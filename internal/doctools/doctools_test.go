package doctools

import (
	"errors"
	"testing"

	. "github.com/onsi/gomega"
	pkgerrors "github.com/pkg/errors"
)

func TestUnavailable(t *testing.T) {
	RegisterTestingT(t)

	reason := errors.New("pandoc not found in $PATH")
	err := pkgerrors.Wrap(Unavailable(reason), "failed to convert")

	Expect(errors.Is(err, ErrUnavailable)).To(BeTrue(), "survives wrapping by callers")
	Expect(errors.Is(err, reason)).To(BeTrue(), "keeps the original cause")
	Expect(err.Error()).To(ContainSubstring("DOCUMENT_TOOLS=on"))
	Expect(err.Error()).To(ContainSubstring("pandoc not found in $PATH"))

	Expect(errors.Is(pkgerrors.New("unrelated"), ErrUnavailable)).To(BeFalse())
}
