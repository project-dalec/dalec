---
title: Buildkit Drivers
---

Dalec builds use Docker Buildkit under the hood. Buildkit supports different drivers that determine where and how builds are executed. Understanding these drivers can help you choose the right setup for your build environment, especially when building at scale or in containerized environments like Kubernetes.

## What are Buildkit Drivers?

Buildkit drivers are different backends that execute the build operations. Each driver has different capabilities and is suitable for different use cases:

- **[docker](https://docs.docker.com/build/builders/drivers/docker/)**: The default driver that runs builds in the Docker daemon
- **[docker-container](https://docs.docker.com/build/builders/drivers/docker-container/)**: Runs builds in an isolated container with more features
- **[kubernetes](https://docs.docker.com/build/builders/drivers/kubernetes/)**: Runs builds as pods in a Kubernetes cluster
- **[remote](https://docs.docker.com/build/builders/drivers/remote/)**: Connects to a remote buildkit daemon

For a complete list of available drivers, see the [official Docker documentation on buildkit drivers](https://docs.docker.com/build/builders/drivers/).

## Kubernetes Driver

The [Kubernetes driver](https://docs.docker.com/build/builders/drivers/kubernetes/) is particularly useful when you want to run builds inside a Kubernetes cluster. This is beneficial for:

- **Scalability**: Distribute builds across multiple nodes
- **Resource isolation**: Each build runs in its own pod with defined resource limits
- **Security**: Builds run in isolated containers with proper security contexts
- **Integration**: Native integration with Kubernetes RBAC, secrets, and networking

### Setting up the Kubernetes Driver

2. **Create a builder instance**:
   ```bash
   docker buildx create \
     --driver kubernetes \
     --name k8s-builder \
     --bootstrap \
     --use
   ```

3. **Build with Dalec using the Kubernetes driver**:
   ```bash
   docker buildx build \
     -t my-package:latest \
     -f my-spec.yml \
     --target=azl3 \
     .
   ```
