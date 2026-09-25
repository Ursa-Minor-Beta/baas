package service

import (
	"context"
	"reflect"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
)

// Every method must fail with an explanation rather than panic or return a
// silent zero value. Driven off the interface by reflection so a method added
// to Pandoc later cannot quietly skip the check.
func Test_unavailablePandoc_everyMethodExplainsItself(t *testing.T) {
	RegisterTestingT(t)

	svc := newUnavailablePandoc(errors.New("pandoc not found in $PATH"))
	v := reflect.ValueOf(svc)
	iface := reflect.TypeOf((*Pandoc)(nil)).Elem()
	Expect(iface.NumMethod()).To(BeNumerically(">", 0))

	for i := 0; i < iface.NumMethod(); i++ {
		m := iface.Method(i)
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
			last := out[len(out)-1].Interface()
			Expect(last).NotTo(BeNil(), "must return an error")

			err, ok := last.(error)
			Expect(ok).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("DOCUMENT_TOOLS=on"),
				"the error has to say how to get the toolchain back")
			Expect(err.Error()).To(ContainSubstring("pandoc not found in $PATH"),
				"the original lookup failure has to survive")
		})
	}
}
