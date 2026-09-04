package service

import (
	"context"
	"net/http"
	"os"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

func TestNewPandoc(t *testing.T) {
	RegisterTestingT(t)
	requireBinary(t, "pandoc")

	tests := []struct {
		name string
		html string
		want string
	}{
		{
			name: "happy path with simple header",
			html: "<html><head></head><body><h1>Hello</h1></body></html>",
			want: "# Hello",
		},
		{
			name: "happy path with simple list",
			html: "<html><head></head><body><ul><li>item1</li><li>item2</li></ul></body></html>",
			want: "-   item1\n-   item2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			got, err := NewPandoc()
			Expect(err).To(BeNil())

			res, err := got.HtmlToMarkdown(ctx, tt.html, "markdown")
			Expect(err).To(BeNil())

			Expect(res).To(Equal(tt.want))
		})
	}
}

func TestParseXls(t *testing.T) {
	RegisterTestingT(t)

	requireBinary(t, "pandoc")
	if os.Getenv("MONGO_URI") == "" {
		t.Skip("MONGO_URI is not set; this test builds a full server against MongoDB and a storage service")
	}

	t.Run("happy path with simple xls", func(t *testing.T) {
		ctx := context.Background()
		s, err := New(ctx)
		Expect(err).To(BeNil())

		xlsData, err := os.ReadFile("./test_data/test.xls")
		Expect(err).To(BeNil())

		uploadResult, err := s.storage.Upload(ctx, xlsData, "application/vnd.ms-excel")
		Expect(err).To(BeNil())
		url := uploadResult.Link

		resp, err := http.Get(url)
		Expect(err).To(BeNil())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		reader := resp.Body

		cfg := &dto.ReadabilityConfig{
			URL:            url,
			ParseTableData: lo.ToPtr(true),
			ReturnPandoc:   lo.ToPtr("gfm-raw_html"),
			DoNotUseCache:  lo.ToPtr(true),
		}
		result, err := s.parseDocument(ctx, cfg, reader, "xls")
		Expect(err).To(BeNil())
		Expect(*result.Pandoc).To(ContainSubstring("First Name"))
		Expect(*result.Pandoc).To(ContainSubstring("Last Name"))
		Expect(*result.Pandoc).NotTo(ContainSubstring("Third Name"))
		Expect(result.Tables).To(HaveLen(1))
		Expect(result.Tables[0].Name).To(Equal("Sheet1"))
		Expect(result.Tables[0].Headers).To(HaveLen(8))
		Expect(result.Tables[0].Rows).To(HaveLen(9))
	})
}
