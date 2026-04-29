package k8s

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

func GetTritonRuntime(name, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "serving.kserve.io/v1alpha1",
			"kind":       "ServingRuntime",
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]any{
				"builtInAdapter": map[string]any{
					"memBufferBytes":            134217728,
					"modelLoadingTimeoutMillis": 90000,
					"runtimeManagementPort":     8001,
					"serverType":                "triton",
				},
				"containers": []any{
					map[string]any{
						"name":  "triton",
						"image": "nvcr.io/nvidia/tritonserver:23.04-py3",
						"command": []any{
							"/bin/sh",
						},
						"args": []any{
							"-c",
							"mkdir -p /models/_triton_models; chmod 777 /models/_triton_models; exec tritonserver \"--model-repository=/models/_triton_models\" \"--model-control-mode=explicit\" \"--strict-model-config=false\" \"--strict-readiness=false\" \"--allow-http=true\" \"--allow-sagemaker=false\" ",
						},
						"resources": map[string]any{
							"requests": map[string]any{
								"cpu":    "500m",
								"memory": "1Gi",
							},
							"limits": map[string]any{
								"cpu":    "4",
								"memory": "4Gi",
							},
						},
						"livenessProbe": map[string]any{
							"exec": map[string]any{
								"command": []any{
									"curl", "--fail", "--silent", "--show-error", "--max-time", "9", "http://localhost:8000/v2/health/live",
								},
							},
							"initialDelaySeconds": 5,
							"periodSeconds":       30,
							"timeoutSeconds":      10,
						},
					},
				},
				"grpcDataEndpoint": "port:8001",
				"grpcEndpoint":     "port:8085",
				"multiModel":       true,
				"protocolVersions": []any{
					"grpc-v2",
				},
				"supportedModelFormats": []any{
					map[string]any{"name": "keras", "version": "2", "autoSelect": true},
					map[string]any{"name": "onnx", "version": "1", "autoSelect": true},
					map[string]any{"name": "pytorch", "version": "1", "autoSelect": true},
					map[string]any{"name": "tensorflow", "version": "1", "autoSelect": true},
					map[string]any{"name": "tensorflow", "version": "2", "autoSelect": true},
					map[string]any{"name": "tensorrt", "version": "7", "autoSelect": true},
				},
			},
		},
	}
}
