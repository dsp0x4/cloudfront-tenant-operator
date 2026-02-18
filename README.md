# cloudfront-tenant-operator

> **Work in Progress** -- This project is under active development. APIs and behavior may change without notice.

A Kubernetes operator for managing [CloudFront Distribution Tenants](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/distribution-tenants.html) -- the AWS CloudFront multi-tenant content delivery feature.

**[Full Documentation](https://dsp0x4.github.io/cloudfront-tenant-operator/)**

## What It Does

The operator manages `DistributionTenant` custom resources that map 1:1 to AWS CloudFront distribution tenants. It handles full lifecycle management (create, update, delete), drift detection, managed certificate tracking, pre-flight validation, and automatic disable-before-delete.

## Quickstart

```sh
# Install CRDs
make install

# Run locally
make run
```

```yaml
apiVersion: cloudfront-tenant-operator.io/v1alpha1
kind: DistributionTenant
metadata:
  name: my-tenant
spec:
  distributionId: "E1XNX8R2GOAABC"
  domains:
    - domain: "my-tenant.example.com"
```

```sh
kubectl apply -f config/samples/cloudfront_v1alpha1_distributiontenant.yaml
kubectl get distributiontenants -w
```

## Documentation

- [Installation](https://dsp0x4.github.io/cloudfront-tenant-operator/getting-started/installation/)
- [Quickstart](https://dsp0x4.github.io/cloudfront-tenant-operator/getting-started/quickstart/)
- [CRD Reference](https://dsp0x4.github.io/cloudfront-tenant-operator/reference/crd/)
- [Architecture](https://dsp0x4.github.io/cloudfront-tenant-operator/architecture/overview/)
- [Contributing](https://dsp0x4.github.io/cloudfront-tenant-operator/contributing/)

## Development

```sh
make test              # Run tests
make lint              # Run linter
make manifests generate  # Regenerate CRDs after editing types
```

## License

Copyright 2026. Licensed under the Apache License, Version 2.0.
