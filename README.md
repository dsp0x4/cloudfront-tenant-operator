# cloudfront-tenant-operator

[![Release](https://github.com/dsp0x4/cloudfront-tenant-operator/actions/workflows/release.yml/badge.svg)](https://github.com/dsp0x4/cloudfront-tenant-operator/actions/workflows/release.yml)
[![Tests](https://github.com/dsp0x4/cloudfront-tenant-operator/actions/workflows/test.yml/badge.svg)](https://github.com/dsp0x4/cloudfront-tenant-operator/actions/workflows/test.yml)
[![Lint](https://github.com/dsp0x4/cloudfront-tenant-operator/actions/workflows/lint.yml/badge.svg)](https://github.com/dsp0x4/cloudfront-tenant-operator/actions/workflows/lint.yml)
[![GitHub Release](https://img.shields.io/github/v/release/dsp0x4/cloudfront-tenant-operator)](https://github.com/dsp0x4/cloudfront-tenant-operator/releases/latest)
[![License](https://img.shields.io/github/license/dsp0x4/cloudfront-tenant-operator)](LICENSE)

> **Work in Progress** -- This project is under active development. APIs and behavior may change without notice.

A Kubernetes operator for managing [CloudFront Distribution Tenants](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/distribution-tenants.html) -- the AWS CloudFront multi-tenant content delivery feature.

**[Full Documentation](https://cloudfront-tenant-operator.io/)**

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

- [Installation](https://cloudfront-tenant-operator.io/getting-started/installation/)
- [Quickstart](https://cloudfront-tenant-operator.io/getting-started/quickstart/)
- [CRD Reference](https://cloudfront-tenant-operator.io/reference/crd/)
- [Architecture](https://cloudfront-tenant-operator.io/architecture/overview/)
- [Contributing](https://cloudfront-tenant-operator.io/contributing/)

## Disclaimer

This project is not affiliated with, endorsed by, or sponsored by Amazon Web Services (AWS). All AWS service names and trademarks are the property of Amazon.com, Inc. or its affiliates.

## License

Copyright 2026. Licensed under the Apache License, Version 2.0.
