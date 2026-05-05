allow_k8s_contexts(['docker-desktop', 'minikube', 'colima', 'kind-kind'])
update_settings(suppress_unused_image_warnings=['load-k6'])

models = read_yaml('models.yaml')['models']

def model_res(name):
    return 'model:' + name

def model_upload_res(name):
    return 'model-upload:' + name

local_resource('metrics-server',
    cmd='./scripts/metrics_server.sh',
    deps=['scripts/metrics_server.sh'],
    labels=['infra']
)

k8s_yaml([
    'k8s/00-namespace.yaml',
    'k8s/10-postgres.yaml',
    'k8s/20-grafana.yaml',
    'k8s/21-grafana-dashboards.yaml',
    'k8s/25-jaeger.yaml',
])

docker_build('loadtestd', '.', dockerfile='Dockerfile')
docker_build('load-k6', '.', dockerfile='Dockerfile.k6')

watch_file('k8s/30-app.yaml')
k8s_yaml(local('envsubst < k8s/30-app.yaml', quiet=True))

local_resource('modelmesh',
    cmd='./scripts/modelmesh_up.sh',
    deps=['scripts/lib.sh', 'scripts/modelmesh_up.sh'],
    labels=['infra', 'setup']
)
watch_file('k8s/triton-model.yaml')
for m in models:
    name = m['name']
    file = m['file']
    path = m.get('path', file)
    local_resource(model_upload_res(name),
        cmd='./scripts/upload_resnet.sh',
        deps=['scripts/lib.sh', 'scripts/upload_resnet.sh', file],
        env={'MODEL_NAME': name, 'MODEL_FILE': file, 'MODEL_PATH': path, 'OBJECT_PATH': path},
        resource_deps=['modelmesh'],
        labels=['setup']
    )
    k8s_yaml(local('envsubst < k8s/triton-model.yaml',
        env={'MODEL_NAME': name, 'MODEL_PATH': path},
        quiet=True))

k8s_resource(new_name='loadtest-system',
             objects=['loadtest-system:namespace'],
             labels=['infra'],
             pod_readiness='ignore')
k8s_resource(workload='loadtestd',
             objects=['loadtestd:serviceaccount', 'loadtestd:clusterrole', 'loadtestd:clusterrolebinding'],
             port_forwards=8080,
             labels=['core'],
             resource_deps=['loadtest-system', 'postgres'])
k8s_resource(workload='grafana',
             objects=['grafana-datasources:configmap:loadtest-system', 'grafana-dashboards:configmap:loadtest-system', 'grafana-dashboards-provider:configmap:loadtest-system'],
             port_forwards=3000,
             labels=['infra'],
             resource_deps=['loadtest-system'],
             links=['http://localhost:3000/d/load-tests/load-tests'])
k8s_resource(workload='jaeger', port_forwards=16686, labels=['infra'],
             resource_deps=['loadtest-system'],
             links=['http://localhost:16686'])
k8s_resource(workload='postgres', port_forwards=5432, labels=['infra'],
             resource_deps=['loadtest-system'])
for m in models:
    name = m['name']
    path = m.get('path', m['file'])
    k8s_resource(new_name=model_res(name),
                 objects=['%s:inferenceservice:modelmesh-serving' % name],
                 labels=['setup'],
                 pod_readiness='ignore',
                 resource_deps=[model_upload_res(name)])
    local_resource('test:search:' + name,
        cmd='./scripts/start_search.sh',
        env={'MODEL_NAME': name, 'MODEL_PATH': path},
        resource_deps=['loadtestd', model_res(name)],
        auto_init=False,
        labels=['test']
    )
    local_resource('test:fixed:' + name,
        cmd='./scripts/start_fixed.sh',
        env={'MODEL_NAME': name, 'MODEL_PATH': path},
        resource_deps=['loadtestd', model_res(name)],
        auto_init=False,
        labels=['test']
    )
