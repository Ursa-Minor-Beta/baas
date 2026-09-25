package pdf

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/Ursa-Minor-Beta/baas/internal/doctools"
)

// fakePython writes an interpreter stand-in that runs the given shell body.
func fakePython(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "python")
	Expect(os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755)).To(Succeed())
	return path
}

func existingScript(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mupdf.py")
	Expect(os.WriteFile(path, nil, 0o644)).To(Succeed())
	return path
}

func TestProbeParser(t *testing.T) {
	RegisterTestingT(t)
	ctx := context.Background()

	t.Run("modules importable", func(t *testing.T) {
		RegisterTestingT(t)
		Expect(ProbeParser(ctx, fakePython(t, "exit 0"), existingScript(t))).To(Succeed())
	})

	t.Run("module missing", func(t *testing.T) {
		RegisterTestingT(t)
		py := fakePython(t, `echo "Traceback (most recent call last):" >&2
echo "ModuleNotFoundError: No module named 'pymupdf'" >&2
exit 1`)
		err := ProbeParser(ctx, py, existingScript(t))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("No module named 'pymupdf'"),
			"the last traceback line names the module")
		Expect(err.Error()).NotTo(ContainSubstring("Traceback"))
	})

	t.Run("asks for every module the script imports", func(t *testing.T) {
		RegisterTestingT(t)
		args := filepath.Join(t.TempDir(), "args")
		py := fakePython(t, `printf '%s' "$2" > `+args)
		Expect(ProbeParser(ctx, py, existingScript(t))).To(Succeed())
		got, err := os.ReadFile(args)
		Expect(err).To(BeNil())
		Expect(string(got)).To(Equal("import pymupdf, PIL, openpyxl"))
	})

	t.Run("script missing", func(t *testing.T) {
		RegisterTestingT(t)
		err := ProbeParser(ctx, fakePython(t, "exit 0"), filepath.Join(t.TempDir(), "nope.py"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("nope.py"))
	})

	t.Run("interpreter missing", func(t *testing.T) {
		RegisterTestingT(t)
		err := ProbeParser(ctx, filepath.Join(t.TempDir(), "no-python"), existingScript(t))
		Expect(err).To(HaveOccurred())
	})
}

func TestUnavailableStubs(t *testing.T) {
	RegisterTestingT(t)
	reason := errors.New("qpdf not found")

	res, err := NewUnavailableParser(reason).Parse(context.Background(), []byte("%PDF-"), "pdf")
	Expect(res).To(BeNil())
	Expect(errors.Is(err, doctools.ErrUnavailable)).To(BeTrue())
	Expect(errors.Is(err, reason)).To(BeTrue())

	paths, cleanup, err := NewUnavailablePdfToImagesConverter(reason).Convert(context.Background(), nil)
	Expect(paths).To(BeNil())
	Expect(cleanup).To(BeNil())
	Expect(errors.Is(err, doctools.ErrUnavailable)).To(BeTrue())
	Expect(err.Error()).To(ContainSubstring("DOCUMENT_TOOLS=on"))
}

// parserModules has to track what mupdf.py actually imports, or the probe
// passes on an image where every parse still fails.
func TestParserModulesMatchScript(t *testing.T) {
	RegisterTestingT(t)
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "scripts", "mupdf.py"))
	Expect(err).To(BeNil())

	stdlib := map[string]bool{
		"argparse": true, "base64": true, "datetime": true, "io": true,
		"json": true, "sys": true, "uuid": true,
	}
	probed := map[string]bool{}
	for _, m := range parserModules {
		probed[m] = true
	}

	for _, line := range strings.Split(string(src), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || (fields[0] != "import" && fields[0] != "from") {
			continue
		}
		module := strings.Split(fields[1], ".")[0]
		Expect(stdlib[module] || probed[module]).To(BeTrue(),
			"mupdf.py imports %q: add it to parserModules, or to the stdlib list here", module)
	}
}
