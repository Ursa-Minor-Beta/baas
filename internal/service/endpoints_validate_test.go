package service

import (
	"encoding/json"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

func Test_validateProcessConfig(t *testing.T) {
	RegisterTestingT(t)

	tests := []struct {
		name      string
		body      string
		expectErr bool
	}{
		{
			name:      "program nested under browser",
			body:      `{"browser":{"program":"navigate('https://example.com');","timeout":"30s"}}`,
			expectErr: false,
		},
		{
			// The decoder ignores unknown top-level fields, so this binds
			// cleanly with an empty program instead of failing to parse.
			name:      "program at the top level is not seen",
			body:      `{"program":"navigate('https://example.com');","timeout":"30s"}`,
			expectErr: true,
		},
		{
			name:      "empty body",
			body:      `{}`,
			expectErr: true,
		},
		{
			name:      "whitespace-only program",
			body:      `{"browser":{"program":"   \n\t "}}`,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg dto.Config
			Expect(json.Unmarshal([]byte(tt.body), &cfg)).To(Succeed())

			err := validateProcessConfig(&cfg)
			if tt.expectErr {
				Expect(err).NotTo(BeNil())
				// The message has to name the field, since that is the whole
				// point of the check.
				Expect(err.Error()).To(ContainSubstring("browser.program"))
			} else {
				Expect(err).To(BeNil())
			}
		})
	}
}
