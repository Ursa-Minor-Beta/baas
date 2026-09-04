package service

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

func TestPdfMimeType(t *testing.T) {
	RegisterTestingT(t)

	type test struct {
		name      string
		url       string
		expected  string
		mustBePDF bool
	}
	tests := []test{
		{
			name:      "happy-path",
			url:       "https://www.atlantis-press.com/article/25904633.pdf",
			expected:  "application/pdf;charset=utf-8",
			mustBePDF: true,
		},
		{
			name:      "must be text/html mime type",
			url:       "https://github.com/ledongthuc/pdf",
			expected:  "text/html; charset=utf-8",
			mustBePDF: false,
		},
	}
	t.Parallel()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mimeType, resp, err := DetermineMimeType(context.TODO(), &dto.ReadabilityConfig{
				URL: tc.url,
			})
			Expect(err).To(BeNil())
			Expect(mimeType).To(Equal(tc.expected))
			Expect(resp).NotTo(BeNil())
			Expect(IsPdf(mimeType)).To(Equal(tc.mustBePDF))
		})
	}
}
