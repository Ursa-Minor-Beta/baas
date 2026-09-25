package service

import (
	"bytes"
	"context"
	"reflect"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/pkg/errors"

	"github.com/Ursa-Minor-Beta/baas/internal/doctools"
)

// Methods that need only Chrome, so the stand-in serves them for real.
var chromeOnlyPandocMethods = map[string]bool{"HtmlToPdf": true}

// Every other method must fail with an explanation rather than panic or hand
// back a partial result. Driven off the interface by reflection so a method
// added to Pandoc later cannot quietly skip the check.
func Test_unavailablePandoc_everyMethodExplainsItself(t *testing.T) {
	RegisterTestingT(t)

	svc := newUnavailablePandoc(errors.New("pandoc not found in $PATH"))
	v := reflect.ValueOf(svc)
	iface := reflect.TypeOf((*Pandoc)(nil)).Elem()
	Expect(iface.NumMethod()).To(BeNumerically(">", 0))

	for i := 0; i < iface.NumMethod(); i++ {
		m := iface.Method(i)
		if chromeOnlyPandocMethods[m.Name] {
			continue
		}
		t.Run(m.Name, func(t *testing.T) {
			RegisterTestingT(t)
			mt := m.Type
			args := make([]reflect.Value, mt.NumIn())
			for j := 0; j < mt.NumIn(); j++ {
				if mt.In(j) == reflect.TypeOf((*context.Context)(nil)).Elem() {
					args[j] = reflect.ValueOf(context.Background())
				} else {
					args[j] = reflect.Zero(mt.In(j))
				}
			}

			out := v.MethodByName(m.Name).Call(args)
			for k := 0; k < len(out)-1; k++ {
				Expect(out[k].IsZero()).To(BeTrue(), "result %d must be the zero value", k)
			}
			last := out[len(out)-1].Interface()
			Expect(last).NotTo(BeNil(), "must return an error")

			err, ok := last.(error)
			Expect(ok).To(BeTrue())
			Expect(errors.Is(err, doctools.ErrUnavailable)).To(BeTrue(),
				"handlers map this sentinel to 501")
			Expect(err.Error()).To(ContainSubstring("DOCUMENT_TOOLS=on"),
				"the error has to say how to get the toolchain back")
			Expect(err.Error()).To(ContainSubstring("pandoc not found in $PATH"),
				"the original lookup failure has to survive")
		})
	}
}

func Test_unavailablePandoc_htmlToPdfStillWorks(t *testing.T) {
	requireChrome(t)
	RegisterTestingT(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	svc := newUnavailablePandoc(errors.New("pandoc not found in $PATH"))
	out, err := svc.HtmlToPdf(ctx, []byte("<html><body><h1>slim</h1></body></html>"), nil)
	Expect(err).To(BeNil())
	Expect(bytes.HasPrefix(out, []byte("%PDF-"))).To(BeTrue(), "expected a PDF, got %d bytes", len(out))
}
