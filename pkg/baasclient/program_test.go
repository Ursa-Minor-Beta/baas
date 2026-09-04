package baasclient

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

type testReporter struct{}

func (r *testReporter) Report(msg string) {
	fmt.Println(msg)
}

func TestProgram(t *testing.T) {
	RegisterTestingT(t)

	baasURL := os.Getenv("BAAS_URL")
	if baasURL == "" {
		t.Skip("BAAS_URL is not set; start a BaaS instance and point BAAS_URL at it to run this test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
	defer cancel()
	p, err := NewProgram(ctx, Config{
		UseProxy:       false,
		LocalDebug:     false,
		Url:            baasURL,
		ApiKey:         os.Getenv("BAAS_API_KEY"),
		Timeout:        "300s",
		MessageTimeout: "60s",
	}, &testReporter{})
	Expect(err).To(BeNil())

	s, err := p.NavigateStatus("https://google.com")
	Expect(err).To(BeNil())
	Expect(s).To(Equal(200))

	sceenshot, err := p.TakeScreenshot("google")
	Expect(err).To(BeNil())
	Expect(sceenshot).NotTo(BeEmpty())

	err = p.LlmSetValue("Search textarea", "What is LLM?\\n")
	Expect(err).To(BeNil())

	sceenshot, err = p.TakeScreenshot("search")
	Expect(err).To(BeNil())
	Expect(sceenshot).NotTo(BeEmpty())
}
