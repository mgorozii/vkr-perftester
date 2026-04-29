package k8s

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/mgorozii/perftester/internal/domain"
)

var (
	servingRuntimeGVR   = schema.GroupVersionResource{Group: "serving.kserve.io", Version: "v1alpha1", Resource: "servingruntimes"}
	inferenceServiceGVR = schema.GroupVersionResource{Group: "serving.kserve.io", Version: "v1beta1", Resource: "inferenceservices"}
	loadJobResources    = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("4000m"),
			corev1.ResourceMemory: resource.MustParse("4Gi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("4000m"),
			corev1.ResourceMemory: resource.MustParse("4Gi"),
		},
	}
)

type API struct {
	core              kubernetes.Interface
	dyn               dynamic.Interface
	controllerNS      string
	storageSecretName string
	k6Image           string
	webhookURL        string
	httpURL           string
	grpcURL           string
}

type Options struct {
	ControllerNamespace string
	StorageSecretName   string
	K6Image             string
	WebhookURL          string
	HTTPURL             string
	GRPCURL             string
}

func NewAPI(opts Options) (*API, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		loading := clientcmd.NewDefaultClientConfigLoadingRules()
		cfg, err = clientcmd.BuildConfigFromFlags("", loading.GetDefaultFilename())
		if err != nil {
			return nil, err
		}
	}
	core, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &API{
		core:              core,
		dyn:               dyn,
		controllerNS:      opts.ControllerNamespace,
		storageSecretName: opts.StorageSecretName,
		k6Image:           opts.K6Image,
		webhookURL:        opts.WebhookURL,
		httpURL:           opts.HTTPURL,
		grpcURL:           opts.GRPCURL,
	}, nil
}

func (a *API) EnsureInference(ctx context.Context, run domain.Run, n ResourceNames) error {
	if err := a.ensureNamespace(ctx, n.Namespace); err != nil {
		return fmt.Errorf("ensure namespace: %w", err)
	}

	protocol := string(run.Protocol)
	if protocol == "" {
		protocol = string(domain.ProtocolHTTP)
	}

	if err := a.applyServingRuntime(ctx, n.Namespace, n.ServingRuntime, protocol); err != nil {
		return fmt.Errorf("apply serving runtime: %w", err)
	}

	if err := a.applyInferenceService(ctx, n.Namespace, n.ServingRuntime, run.ModelName, run.S3Path, run.ModelFormat); err != nil {
		return fmt.Errorf("apply inference service: %w", err)
	}

	if err := a.waitInferenceServiceReady(ctx, n.Namespace, n.InferenceService, 10*time.Minute); err != nil {
		return fmt.Errorf("wait inference service: %w", err)
	}

	return nil
}

func (a *API) ensureNamespace(ctx context.Context, namespace string) error {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:   namespace,
		Labels: map[string]string{"modelmesh-enabled": "true"},
	}}
	_, err := a.core.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		current, getErr := a.core.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		if current.Labels == nil {
			current.Labels = map[string]string{}
		}
		if current.Labels["modelmesh-enabled"] == "true" {
			return nil
		}
		current.Labels["modelmesh-enabled"] = "true"
		_, err = a.core.CoreV1().Namespaces().Update(ctx, current, metav1.UpdateOptions{})
	}
	return err
}

func (a *API) applyServingRuntime(ctx context.Context, namespace, name, _ string) error {
	if err := a.copyStorageSecret(ctx, namespace); err != nil {
		return err
	}
	obj := GetTritonRuntime(name, namespace)
	_, err := a.dyn.Resource(servingRuntimeGVR).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		current, getErr := a.dyn.Resource(servingRuntimeGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		obj.SetResourceVersion(current.GetResourceVersion())
		_, err = a.dyn.Resource(servingRuntimeGVR).Namespace(namespace).Update(ctx, obj, metav1.UpdateOptions{})
	}
	return err
}

func (a *API) applyInferenceService(ctx context.Context, namespace, runtimeName, modelName, s3Path, modelFormat string) error {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "serving.kserve.io/v1beta1",
		"kind":       "InferenceService",
		"metadata": map[string]any{
			"name":      modelName,
			"namespace": namespace,
			"annotations": map[string]any{
				"serving.kserve.io/deploymentMode": "ModelMesh",
				"serving.kserve.io/secretKey":      "localMinIO",
			},
		},
		"spec": map[string]any{
			"predictor": map[string]any{
				"model": map[string]any{
					"modelFormat": map[string]any{"name": modelFormat},
					"runtime":     runtimeName,
					"storageUri":  s3Path,
				},
			},
		},
	}}
	_, err := a.dyn.Resource(inferenceServiceGVR).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		current, getErr := a.dyn.Resource(inferenceServiceGVR).Namespace(namespace).Get(ctx, modelName, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		obj.SetResourceVersion(current.GetResourceVersion())
		_, err = a.dyn.Resource(inferenceServiceGVR).Namespace(namespace).Update(ctx, obj, metav1.UpdateOptions{})
	}
	return err
}

func (a *API) CreateLoadJob(ctx context.Context, namespace, jobName, config string, env map[string]string) error {
	cmName := jobName + "-config"
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: namespace},
		Data:       map[string]string{"config.json": config},
	}
	_, err := a.core.CoreV1().ConfigMaps(namespace).Create(ctx, cm, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		_, err = a.core.CoreV1().ConfigMaps(namespace).Update(ctx, cm, metav1.UpdateOptions{})
	}
	if err != nil {
		return err
	}

	var envVars []corev1.EnvVar
	for k, v := range env {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}
	envVars = append(envVars, corev1.EnvVar{Name: "CONFIG_PATH", Value: "/etc/loadtest/config.json"})

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: jobName, Namespace: namespace},
		Spec: batchv1.JobSpec{
			BackoffLimit: new(int32),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:            "executor",
						Image:           a.k6Image,
						ImagePullPolicy: corev1.PullNever,
						Command:         []string{"/usr/bin/executor"},
						Env:             envVars,
						Resources:       loadJobResources,
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "config",
							MountPath: "/etc/loadtest",
						}},
					}},
					Volumes: []corev1.Volume{{
						Name: "config",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
								Items: []corev1.KeyToPath{{
									Key:  "config.json",
									Path: "config.json",
								}},
							},
						},
					}},
				},
			},
		},
	}
	_, err = a.core.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		if err := a.core.BatchV1().Jobs(namespace).Delete(ctx, jobName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		_, err = a.core.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
	}
	return err
}

func (a *API) waitInferenceServiceReady(ctx context.Context, namespace, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		obj, err := a.dyn.Resource(inferenceServiceGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
		if err != nil {
			return err
		}
		if found {
			for _, item := range conditions {
				condition, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if condition["type"] == "Ready" && condition["status"] == "True" {
					return nil
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timeout waiting for inferenceservice %s/%s ready", namespace, name)
}

func (a *API) DeleteNamespace(ctx context.Context, namespace string) error {
	err := a.core.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (a *API) copyStorageSecret(ctx context.Context, namespace string) error {
	secret, err := a.core.CoreV1().Secrets(a.controllerNS).Get(ctx, a.storageSecretName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	copy := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secret.Name, Namespace: namespace},
		Type:       secret.Type,
		Data:       secret.Data,
		StringData: secret.StringData,
	}
	_, err = a.core.CoreV1().Secrets(namespace).Create(ctx, copy, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		current, getErr := a.core.CoreV1().Secrets(namespace).Get(ctx, secret.Name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		copy.ResourceVersion = current.ResourceVersion
		_, err = a.core.CoreV1().Secrets(namespace).Update(ctx, copy, metav1.UpdateOptions{})
	}
	return err
}
