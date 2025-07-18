---
title: Buildkit Drivers
---

Dalec builds use Docker Buildkit under the hood. Buildkit supports different drivers that determine where and how builds are executed. Understanding these drivers can help you choose the right setup for your build environment, especially when building at scale or in containerized environments like Kubernetes.

## What are Buildkit Drivers?

Buildkit drivers are different backends that execute the build operations. Each driver has different capabilities and is suitable for different use cases:

- **docker**: The default driver that runs builds in the Docker daemon
- **docker-container**: Runs builds in an isolated container with more features
- **kubernetes**: Runs builds as pods in a Kubernetes cluster
- **remote**: Connects to a remote buildkit daemon

For a complete list of available drivers, see the [official Docker documentation on buildkit drivers](https://docs.docker.com/build/builders/drivers/).

## Default Docker Driver

By default, Dalec uses the standard Docker driver through `docker build` or `docker buildx build`. This works well for local development and simple CI/CD scenarios.

```bash
# Using the default docker driver
docker build -t my-package:latest -f my-spec.yml --target=mariner2 .
```

## Kubernetes Driver

The Kubernetes driver is particularly useful when you want to run builds inside a Kubernetes cluster. This is beneficial for:

- **Scalability**: Distribute builds across multiple nodes
- **Resource isolation**: Each build runs in its own pod with defined resource limits
- **Security**: Builds run in isolated containers with proper security contexts
- **Integration**: Native integration with Kubernetes RBAC, secrets, and networking

### Setting up the Kubernetes Driver

1. **Prerequisites**:
   - A running Kubernetes cluster with `kubectl` access
   - Docker Buildx CLI installed
   - Appropriate RBAC permissions to create pods and services

2. **Create a builder instance**:
   ```bash
   docker buildx create \
     --driver kubernetes \
     --name k8s-builder \
     --bootstrap
   ```

3. **Use the Kubernetes builder**:
   ```bash
   docker buildx use k8s-builder
   ```

4. **Build with Dalec using the Kubernetes driver**:
   ```bash
   docker buildx build \
     -t my-package:latest \
     -f my-spec.yml \
     --target=mariner2 \
     .
   ```

### Advanced Kubernetes Driver Configuration

You can customize the Kubernetes driver behavior with additional options:

```bash
# Create builder with custom configuration
docker buildx create \
  --driver kubernetes \
  --driver-opt namespace=build-namespace \
  --driver-opt requests.cpu=1 \
  --driver-opt requests.memory=2Gi \
  --driver-opt limits.cpu=2 \
  --driver-opt limits.memory=4Gi \
  --name k8s-builder-custom
```

#### Available Driver Options

- `namespace`: Kubernetes namespace for build pods (default: current namespace)
- `requests.cpu`/`requests.memory`: Resource requests for build pods
- `limits.cpu`/`limits.memory`: Resource limits for build pods
- `nodeselector`: Node selector for pod placement
- `tolerations`: Tolerations for pod scheduling
- `serviceaccount`: Service account for build pods

### Kubernetes Driver with Custom Resources

For production use, you may want to create a dedicated service account and namespace:

```yaml
# build-namespace.yaml
apiVersion: v1
kind: Namespace
metadata:
  name: dalec-builds
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: dalec-builder
  namespace: dalec-builds
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  namespace: dalec-builds
  name: dalec-builder-role
rules:
- apiGroups: [""]
  resources: ["pods", "pods/log"]
  verbs: ["create", "get", "list", "delete"]
- apiGroups: [""]
  resources: ["pods/exec"]
  verbs: ["create"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: dalec-builder-binding
  namespace: dalec-builds
subjects:
- kind: ServiceAccount
  name: dalec-builder
  namespace: dalec-builds
roleRef:
  kind: Role
  name: dalec-builder-role
  apiGroup: rbac.authorization.k8s.io
```

Apply the configuration:
```bash
kubectl apply -f build-namespace.yaml
```

Create the builder with the custom service account:
```bash
docker buildx create \
  --driver kubernetes \
  --driver-opt namespace=dalec-builds \
  --driver-opt serviceaccount=dalec-builder \
  --name dalec-k8s-builder
```

### Benefits of Using Kubernetes Driver with Dalec

1. **Parallel Builds**: Run multiple Dalec builds concurrently across different nodes
2. **Resource Management**: Set CPU and memory limits for builds to prevent resource contention
3. **Secrets Integration**: Use Kubernetes secrets for build-time authentication
4. **Network Policies**: Apply network policies to control build-time network access
5. **Persistent Volumes**: Mount persistent volumes for build caches
6. **Multi-architecture**: Build for different architectures using node affinity

### Example: Building with Resource Limits

```bash
# Create a builder with specific resource constraints
docker buildx create \
  --driver kubernetes \
  --driver-opt namespace=dalec-builds \
  --driver-opt requests.cpu=500m \
  --driver-opt requests.memory=1Gi \
  --driver-opt limits.cpu=2 \
  --driver-opt limits.memory=4Gi \
  --name resource-limited-builder

# Use it for a Dalec build
docker buildx use resource-limited-builder
docker buildx build \
  -f my-dalec-spec.yml \
  --target=ubuntu/jammy \
  -t my-package:latest \
  .
```

## Troubleshooting

### Common Issues with Kubernetes Driver

1. **Permission Denied**: Ensure your service account has the necessary RBAC permissions
2. **Resource Constraints**: Check if your cluster has sufficient resources
3. **Network Issues**: Verify that build pods can access required external resources
4. **Image Pull Errors**: Ensure image pull secrets are configured if using private registries

### Debugging Build Issues

View build pod logs:
```bash
kubectl logs -n dalec-builds -l builder=dalec-k8s-builder
```

Check pod status:
```bash
kubectl get pods -n dalec-builds -l builder=dalec-k8s-builder
```

## Further Reading

- [Docker Buildkit Drivers Documentation](https://docs.docker.com/build/builders/drivers/)
- [Kubernetes Driver Specific Documentation](https://docs.docker.com/build/builders/drivers/kubernetes/)
- [Docker Buildx Reference](https://docs.docker.com/engine/reference/commandline/buildx_create/)