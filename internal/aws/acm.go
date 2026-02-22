/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package aws

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/acm"
)

// acmAPI defines the subset of the AWS ACM SDK client we use.
type acmAPI interface {
	DescribeCertificate(ctx context.Context, params *acm.DescribeCertificateInput, optFns ...func(*acm.Options)) (*acm.DescribeCertificateOutput, error)
}

// RealACMClient is the production implementation of ACMClient that
// delegates to the AWS ACM SDK.
type RealACMClient struct {
	api acmAPI
}

// NewRealACMClient creates a new RealACMClient using the provided AWS config.
func NewRealACMClient(cfg aws.Config) *RealACMClient {
	return &RealACMClient{
		api: acm.NewFromConfig(cfg),
	}
}

func (c *RealACMClient) GetCertificateSANs(ctx context.Context, certificateArn string) ([]string, error) {
	start := time.Now()
	defer observeAWSLatency("ACMDescribeCertificate", start)

	out, err := c.api.DescribeCertificate(ctx, &acm.DescribeCertificateInput{
		CertificateArn: aws.String(certificateArn),
	})
	if err != nil {
		return nil, classifyACMError(err)
	}

	if out.Certificate == nil {
		return nil, nil
	}

	return out.Certificate.SubjectAlternativeNames, nil
}
