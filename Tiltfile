# Tiltfile

# Configure the local registry
# Host pushes to localhost:5001
# Cluster pulls from kind-registry:5000
default_registry('localhost:5001', host_from_cluster='kind-registry:5000')

# Define the image name and tag
image_name = 'ingress2gateway-controller'
image_tag = 'latest'
full_image_name = 'localhost:5001/' + image_name

# Build the Docker image
# Tilt will use its content-based hash as the primary tag
# and also push with 'latest' tag via extra_tag
docker_build(
    full_image_name,
    '.',
    dockerfile='Dockerfile.dev',
    ignore=['helm', 'examples', 'controller.exe', '.git', 'bin'],
    extra_tag=full_image_name + ':' + image_tag
)

# Deploy using Helm
# Pass the same tag to Helm so the deployment references the correct image
k8s_yaml(helm(
    'helm/ingress2gateway-controller',
    name='ingress2gateway-controller',
    set=[
        'image.registry=localhost:5001',
        'image.repository=' + image_name,
        'image.tag=' + image_tag,
        'image.pullPolicy=Always',
    ]
))

# Watch for changes in the helm directory to re-deploy manifest
# (Built-in to helm() but good to know)

# Port forward for metrics/health if useful
k8s_resource('ingress2gateway-controller', port_forwards='8080:8080')
