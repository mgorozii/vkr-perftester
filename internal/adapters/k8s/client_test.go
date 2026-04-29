package k8s

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCreateLoadJobSetsExecutorResources(t *testing.T) {
	api := &API{core: fake.NewSimpleClientset()}
	ctx := context.Background()

	if _, err := api.core.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	if err := api.CreateLoadJob(ctx, "test", "job", "{}", nil); err != nil {
		t.Fatal(err)
	}

	job, err := api.core.BatchV1().Jobs("test").Get(ctx, "job", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c := job.Spec.Template.Spec.Containers[0]
	if got := c.Resources.Requests.Cpu().String(); got != "4" {
		t.Fatalf("cpu request=%s", got)
	}
	if got := c.Resources.Limits.Cpu().String(); got != "4" {
		t.Fatalf("cpu limit=%s", got)
	}
	if got := c.Resources.Requests.Memory().String(); got != "4Gi" {
		t.Fatalf("memory request=%s", got)
	}
	if got := c.Resources.Limits.Memory().String(); got != "4Gi" {
		t.Fatalf("memory limit=%s", got)
	}
}
