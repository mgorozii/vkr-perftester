package httpapi

import (
	"cmp"
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	sloggin "github.com/samber/slog-gin"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/mgorozii/perftester/internal/app"
	"github.com/mgorozii/perftester/internal/domain"
)

//go:embed templates/*
var templates embed.FS

type Server struct {
	logger *slog.Logger
	svc    *app.Service
}

func New(logger *slog.Logger, svc *app.Service) *Server { return &Server{logger: logger, svc: svc} }

func (s *Server) Handler() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(sloggin.NewWithConfig(s.logger, sloggin.Config{
		Filters: []sloggin.Filter{
			func(c *gin.Context) bool {
				return c.Request.URL.Path != "/healthz" && c.Request.URL.Path != "/readyz"
			},
		},
	}))
	r.Use(gin.Recovery())

	r.SetHTMLTemplate(template.Must(template.New("report.gohtml").Funcs(template.FuncMap{
		"lower": strings.ToLower,
	}).ParseFS(templates, "templates/report.gohtml")))

	v1 := r.Group("/api/v1")
	{
		v1.POST("/tests:start", s.startTest)
		v1.POST("/report", s.acceptReport)
		v1.POST("/search_steps", s.acceptSearchSteps)
		v1.POST("/run_status", s.acceptRunStatus)
		v1.GET("/runs/:id", s.getRun)
		v1.POST("/runs/:id/cancel", s.cancelRun)
	}

	r.POST("/variable", s.variable)
	r.GET("/api/v1/grafana/report.html", s.reportHTML)

	r.GET("/healthz", s.health)
	r.GET("/readyz", s.health)
	r.GET("/", s.health)

	return r
}

func (s *Server) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) startTest(c *gin.Context) {
	var req struct {
		Tenant       string `json:"tenant"`
		Name         string `json:"name"`
		S3Path       string `json:"s3_path"`
		TargetRPS    *int32 `json:"target_rps"`
		Duration     string `json:"duration"`
		Payload      string `json:"payload"`
		Protocol     string `json:"protocol"`
		MaxLatencyMS *int32 `json:"max_latency_ms"`
		ModelFormat  string `json:"model_format"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var d *time.Duration
	if req.Duration != "" {
		parsed, err := time.ParseDuration(req.Duration)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid duration: " + err.Error()})
			return
		}
		d = &parsed
	}

	id, err := s.svc.StartTest(c.Request.Context(), domain.StartTestCmd{
		Tenant:       req.Tenant,
		Name:         req.Name,
		S3Path:       req.S3Path,
		ModelFormat:  req.ModelFormat,
		TargetRPS:    req.TargetRPS,
		Duration:     d,
		MaxLatencyMS: req.MaxLatencyMS,
		Payload:      req.Payload,
		Protocol:     domain.Protocol(req.Protocol),
	})
	if err != nil {
		s.logger.Error("failed to start test", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"run_id": id})
}

func (s *Server) getRun(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "run id required"})
		return
	}

	run, err := s.svc.GetRun(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	resp := gin.H{
		"run_id": run.ID,
		"status": string(run.Status),
	}
	if run.FailureReason != "" {
		resp["failure_reason"] = run.FailureReason
	}

	if run.Status == domain.StatusSuccess || run.Status == domain.StatusFailed {
		steps, err := s.svc.ListSearchSteps(c.Request.Context(), run.ID)
		if err != nil {
			s.logger.ErrorContext(c.Request.Context(), "list search steps", "run_id", run.ID, "error", err)
		}
		if len(steps) > 0 {
			var maxRPS int
			var p99 float64
			for _, step := range steps {
				if step.RPS > maxRPS && step.StopReason == "" {
					maxRPS = step.RPS
					p99 = step.P99LatencyMS
				}
				if step.StopReason != "" {
					if maxRPS == 0 {
						maxRPS = step.RPS
						p99 = step.P99LatencyMS
					}
					break
				}
			}

			if run.Status == domain.StatusSuccess {
				metrics, err := s.svc.QueryMetrics(c.Request.Context(), app.MetricsFilter{RunID: run.ID, From: time.Unix(0, 0), To: time.Now().Add(time.Hour)})
				if err == nil && len(metrics) > 0 {
					latest := latestMetrics(metrics)
					if finalP99, ok := latest["http_req_duration.p99"]; ok && finalP99 > 0 {
						p99 = finalP99
					}
					if finalRPS, ok := latest["http_reqs.rate"]; ok && finalRPS > 0 {
						maxRPS = int(finalRPS)
					}
				}
			}

			resp["max_rps"] = maxRPS
			resp["p99_latency_ms"] = p99
			resp["steps"] = steps
		}
	}

	c.JSON(http.StatusOK, resp)
}

func (s *Server) cancelRun(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "run id required"})
		return
	}

	if err := s.svc.CancelRun(c.Request.Context(), id); err != nil {
		if errors.Is(err, app.ErrCancelInactive) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Status(http.StatusAccepted)
}

func (s *Server) acceptReport(c *gin.Context) {
	var req struct {
		RunID   string               `json:"run_id"`
		Metrics []domain.MetricPoint `json:"metrics"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := s.svc.AcceptReport(c.Request.Context(), domain.Report{
		RunID:    req.RunID,
		Received: time.Now(),
		Metrics:  req.Metrics,
	})
	if err != nil {
		s.logger.Error("failed to accept report", "run_id", req.RunID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusAccepted)
}

func (s *Server) acceptSearchSteps(c *gin.Context) {
	var req struct {
		RunID string              `json:"run_id"`
		Steps []domain.SearchStep `json:"steps"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := s.svc.AcceptSearchSteps(c.Request.Context(), req.RunID, req.Steps)
	if err != nil {
		s.logger.Error("failed to accept search steps", "run_id", req.RunID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusAccepted)
}

func (s *Server) acceptRunStatus(c *gin.Context) {
	var req struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.RunID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "run_id required"})
		return
	}
	if req.Status != string(domain.StatusFailed) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only FAILED status is supported"})
		return
	}
	if err := s.svc.FailRun(c.Request.Context(), req.RunID, req.Error); err != nil {
		s.logger.Error("failed to accept run status", "run_id", req.RunID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusAccepted)
}

func (s *Server) variable(c *gin.Context) {
	var req struct {
		Payload struct {
			Target string `json:"target"`
		} `json:"payload"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json payload"})
		return
	}
	payloadStr := req.Payload.Target

	parts := strings.Split(payloadStr, "|")
	target := parts[0]
	tenant := ""
	model := ""
	runID := ""
	if len(parts) > 1 {
		tenant = parts[1]
	}
	if len(parts) > 2 {
		model = parts[2]
	}
	if len(parts) > 3 {
		runID = parts[3]
	}

	filter := app.RunFilter{Tenant: tenant, ModelName: model, RunID: runID}
	runs, err := s.svc.ListRuns(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	set := map[string]struct{}{}
	for _, run := range runs {
		switch target {
		case "tenant":
			set[run.Tenant] = struct{}{}
		case "model_name":
			set[run.ModelName] = struct{}{}
		case "run_id":
			set[run.ID] = struct{}{}
		}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		if k != "" {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	out := make([]map[string]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, map[string]string{"text": k, "value": k})
	}

	c.JSON(http.StatusOK, out)
}

type reportCard struct {
	Label string
	Value string
}

type reportRow struct {
	Label string
	Value string
}

type reportSection struct {
	Name string
	Rows []reportRow
}

type reportPage struct {
	Tenant       string
	ModelName    string
	RunID        string
	Status       string
	Protocol     string
	TargetRPS    int32
	MaxLatencyMS int32
	Duration     string
	S3Path       string
	CreatedAt    string
	Cards        []reportCard
	Sections     []reportSection
	HasRun       bool
	HasMetrics   bool
	HasSearch    bool
	SearchSteps  []domain.SearchStep
	SearchLoad   string
	SearchStop   string
	SearchExec   string
	IsReady      bool
}

func (s *Server) reportHTML(c *gin.Context) {
	page, err := s.buildReportPage(c.Request.Context(), c.Request)
	if err != nil {
		s.logger.Error("failed to build report page", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.HTML(http.StatusOK, "report.gohtml", page)
}

func (s *Server) buildReportPage(ctx context.Context, r *http.Request) (reportPage, error) {
	tenant := r.URL.Query().Get("tenant")
	model := r.URL.Query().Get("model_name")
	runID := r.URL.Query().Get("run_id")

	if (tenant == "" || model == "") && runID == "" {
		return reportPage{IsReady: false}, nil
	}

	runs, err := s.svc.ListRuns(ctx, app.RunFilter{Tenant: tenant, ModelName: model, RunID: runID})
	if err != nil {
		return reportPage{}, err
	}
	if len(runs) == 0 {
		return reportPage{IsReady: true, HasRun: false}, nil
	}
	run := runs[0]

	page := reportPage{
		Tenant:    run.Tenant,
		ModelName: run.ModelName,
		RunID:     run.ID,
		Status:    string(run.Status),
		Protocol:  string(run.Protocol),
		S3Path:    run.S3Path,
		CreatedAt: run.CreatedAt.Format(time.RFC3339),
		HasRun:    true,
		IsReady:   true,
	}
	if run.TargetRPS != nil {
		page.TargetRPS = *run.TargetRPS
	}
	if run.MaxLatencyMS != nil {
		page.MaxLatencyMS = *run.MaxLatencyMS
	}
	if run.Duration != nil {
		page.Duration = run.Duration.String()
	}

	steps, err := s.svc.ListSearchSteps(ctx, run.ID)
	if err != nil {
		s.logger.ErrorContext(ctx, "list search steps", "run_id", run.ID, "error", err)
	}
	if len(steps) > 0 {
		latestByRPS := make(map[int]domain.SearchStep)
		for _, s := range steps {
			if existing, ok := latestByRPS[s.RPS]; !ok || s.StepNumber > existing.StepNumber {
				latestByRPS[s.RPS] = s
			}
		}
		steps = make([]domain.SearchStep, 0, len(latestByRPS))
		for _, s := range latestByRPS {
			steps = append(steps, s)
		}

		slices.SortFunc(steps, func(a, b domain.SearchStep) int {
			return cmp.Compare(a.RPS, b.RPS)
		})
		page.HasSearch = true
		page.SearchSteps = steps
		page.SearchLoad = "RPS"
		page.SearchExec = "constant-arrival-rate"
		page.SearchStop = searchStopText(steps, page.SearchLoad)
	}

	metrics, err := s.svc.QueryMetrics(ctx, app.MetricsFilter{RunID: run.ID, From: time.Unix(0, 0).UTC(), To: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		return page, err
	}
	if len(metrics) > 0 {
		page.HasMetrics = true
		latest := latestMetrics(metrics)

		page.Cards = []reportCard{
			{Label: "Iterations", Value: displayMetric("iterations.count", latest["iterations.count"])},
			{Label: "HTTP Requests", Value: displayMetric("http_reqs.count", latest["http_reqs.count"])},
			{Label: "HTTP Fail Rate", Value: displayMetric("http_req_failed.rate", latest["http_req_failed.rate"])},
			{Label: "Test Duration", Value: displayMetric("test_run_duration_ms", latest["test_run_duration_ms"])},
			{Label: "HTTP Avg", Value: displayMetric("http_req_duration.avg", latest["http_req_duration.avg"])},
			{Label: "HTTP p95", Value: displayMetric("http_req_duration.p95", latest["http_req_duration.p95"])},
		}
		page.Sections = buildSections(latest)
	}

	return page, nil
}

func searchStopText(steps []domain.SearchStep, unit string) string {
	maxStable := 0
	stopReason := ""
	stopRPS := 0

	for _, step := range steps {
		if step.StopReason == "" {
			if step.RPS > maxStable {
				maxStable = step.RPS
			}
		} else {
			if stopRPS == 0 || step.RPS < stopRPS {
				stopRPS = step.RPS
				stopReason = step.StopReason
			}
		}
	}

	res := ""
	if maxStable > 0 {
		res = fmt.Sprintf("Max Stable: %d %s", maxStable, unit)
	}
	if stopReason != "" {
		if res != "" {
			res += " / "
		}
		switch stopReason {
		case "latency":
			res += fmt.Sprintf("SLO Limit at %d %s", stopRPS, unit)
		case "error_rate":
			res += fmt.Sprintf("Error Limit at %d %s", stopRPS, unit)
		case "throughput":
			res += fmt.Sprintf("Saturation at %d %s", stopRPS, unit)
		default:
			res += fmt.Sprintf("%s at %d %s", stopReason, stopRPS, unit)
		}
	}
	return res
}

func buildSections(metrics map[string]float64) []reportSection {
	sections := map[string][]reportRow{}
	names := make([]string, 0, len(metrics))
	for name := range metrics {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		sections[metricSection(name)] = append(sections[metricSection(name)], reportRow{Label: metricLabel(name), Value: displayMetric(name, metrics[name])})
	}
	order := []string{"TOTAL", "HTTP", "MODELS", "EXECUTION", "NETWORK", "CHECKS", "CUSTOM"}
	out := make([]reportSection, 0, len(order))
	for _, name := range order {
		if len(sections[name]) == 0 {
			continue
		}
		out = append(out, reportSection{Name: name, Rows: sections[name]})
	}
	return out
}

func latestMetrics(metrics []domain.Metric) map[string]float64 {
	slices.SortFunc(metrics, func(a, b domain.Metric) int {
		if c := cmp.Compare(a.MetricName, b.MetricName); c != 0 {
			return c
		}
		return cmp.Compare(a.Timestamp.UnixNano(), b.Timestamp.UnixNano())
	})
	out := map[string]float64{}
	for _, metric := range metrics {
		out[metric.MetricName] = metric.Value
	}
	return out
}

func metricSection(name string) string {
	switch {
	case strings.HasPrefix(name, "http_"):
		return "HTTP"
	case strings.HasPrefix(name, "inference_"), strings.HasPrefix(name, "grpc_"):
		return "MODELS"
	case strings.HasPrefix(name, "iteration"), strings.HasPrefix(name, "iterations"), strings.HasPrefix(name, "vus"):
		return "EXECUTION"
	case strings.HasPrefix(name, "data_"):
		return "NETWORK"
	case strings.HasPrefix(name, "checks"), strings.HasPrefix(name, "check_"):
		return "CHECKS"
	case strings.HasPrefix(name, "test_"):
		return "TOTAL"
	default:
		return "CUSTOM"
	}
}

func metricLabel(name string) string {
	repl := strings.NewReplacer("_", " ", ".", " ")
	return cases.Title(language.English).String(repl.Replace(name))
}

func displayMetric(name string, value float64) string {
	switch {
	case strings.HasSuffix(name, ".rate"):
		if strings.Contains(name, "failed") || strings.Contains(name, "checks") {
			return strconv.FormatFloat(value*100, 'f', 2, 64) + "%"
		}
		return strconv.FormatFloat(value, 'f', 2, 64) + " RPS"
	case strings.Contains(name, "duration"), strings.HasSuffix(name, "_ms"):
		return strconv.FormatFloat(value, 'f', 2, 64) + " ms"
	default:
		if value == math.Trunc(value) {
			return strconv.FormatInt(int64(value), 10)
		}
		return strconv.FormatFloat(value, 'f', 2, 64)
	}
}
