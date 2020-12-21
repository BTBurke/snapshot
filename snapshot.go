package snapshot

import (
	"bytes"
	"io/ioutil"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/davecgh/go-spew/spew"
	"github.com/pmezard/go-difflib/difflib"
)

// Config holds values for the full path to the snapshot directory and the number of context lines to show with
// snapshot diffs.
type Config struct {
	// Full path to snapshot directory
	Directory string
	// Number of lines of context to show with snapshot diffs
	Context int
	// Whether output is diffable (false for binary file formats)
	Diffable bool
	// Extension of the snap recording test file.  Can be useful if test file is a binary format and you want to inspect it manually (e.g., images).  Defaults to .snap.
	Extension string
	// a regex diff to ignore (for stateful diffs, e.g., creation dates)
	ignore *regexp.Regexp
}

// ConfigOption is a functional option that sets config values
type ConfigOption func(c *Config) error

// New creates a new config.  Options can set the snapshot directory and context.  The snapshot directory defaults to
// __snapshots__ relative to the current working directory.  Default 10 context lines.  Use `snapshot.Assert` directly
// if you don't need to change these defaults.
func New(options ...ConfigOption) (*Config, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	c := &Config{
		Directory: path.Join(wd, "__snapshots__"),
		Context:   10,
		Diffable:  true,
		Extension: ".snap",
	}
	for _, opt := range options {
		if err := opt(c); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// SnapDirectory sets the snapshot directory to the full path given
func SnapDirectory(dir string) ConfigOption {
	return func(c *Config) error {
		c.Directory = dir
		return nil
	}
}

// ContextLines sets the max number of context lines shown before the diff
func ContextLines(n int) ConfigOption {
	return func(c *Config) error {
		c.Context = n
		return nil
	}
}

// Diffable indicates whether a meaningful diff can be shown for the test case.  Defaults to true but can be set
// to false for binary file formats (e.g., images, PDF files, etc.)
func Diffable(b bool) ConfigOption {
	return func(c *Config) error {
		c.Diffable = b
		return nil
	}
}

// IgnoreRegex sets a regex on the diff output which will ignore stateful changes in the output that should not be
// considered test failures.  For example, PDF files embed creation dates that will change from test to test but can
// be ignored with an appropriate regex.
func IgnoreRegex(r string) ConfigOption {
	return func(c *Config) error {
		reg, err := regexp.Compile(r)
		if err != nil {
			return err
		}
		c.ignore = reg
		return nil
	}
}

// SnapExtension changes the extension of the snap test files to `ext` instead of the default .snap
func SnapExtension(ext string) ConfigOption {
	return func(c *Config) error {
		c.Extension = ext
		return nil
	}
}

// Assert compares the output in b to the snapshot saved for the current test.  If the snapshot file does not
// yet exist for this test, it will be created and the test will pass.  If the snapshot file exists and the test
// output does not match, the test will fail and a diff will be shown.  To update your snapshots, set
// `UPDATE_SNAPSHOTS=true` when running your test suite.  The default config stores snapshots in `__snapshots__` relative
// to the test directory.
func Assert(t testing.TB, b interface{}) {
	c, err := New()
	if err != nil {
		t.Fatalf("Unable to create new snapshot config: %s", err)
	}
	switch b.(type) {
	case []byte:
		c.Assert(t, b.([]byte))
	default:
		buf := new(bytes.Buffer)
		scs := spew.ConfigState{
			DisablePointerAddresses: true,
		}
		scs.Fdump(buf, b)
		c.Assert(t, buf.Bytes())
	}

}

// Assert compares the output in b to the snapshot saved for the current test.  If the snapshot file does not
// yet exist for this test, it will be created and the test will pass.  If the snapshot file exists and the test
// output does not match, the test will fail and a diff will be shown.  To update your snapshots, set
// `UPDATE_SNAPSHOTS=true` when running your test suite.
//
// See `New` for custom configuration options such as where to save testing snapshots.
func (c *Config) Assert(t testing.TB, b []byte) {
	t.Helper()

	// if no snapshot directory exists, fail unless updateable is set
	if _, err := os.Stat(c.Directory); os.IsNotExist(err) {
		switch {
		case isUpdateable():
			if err := os.MkdirAll(c.Directory, os.FileMode(0777)); err != nil {
				t.Fatalf("Unable to create the snapshot directory, failing")
			}
			if err := createSnapshot(t.Name(), b, c.Directory, c.Extension); err != nil {
				t.Fatalf("Unable to create snapshot: %s", err)
			}
			return
		default:
			t.Fatalf("No snapshot directory exists and UPDATE_SNAPSHOTS=false.  Failing.")
		}
	}

	expected, err := readSnapshot(t.Name(), c.Directory, c.Extension)
	if err != nil {
		if err := createSnapshot(t.Name(), b, c.Directory, c.Extension); err != nil {
			t.Fatalf("Unable to create snapshot: %s", err)
		}
		return
	}
	switch {
	case bytes.Equal(expected, b):
		return
	default:
		if isUpdateable() {
			if err := createSnapshot(t.Name(), b, c.Directory, c.Extension); err != nil {
				t.Fatalf("Unable to create snapshot: %s", err)
			}
			return
		}
		switch {
		case c.Diffable:
			diff, err := getDiff(expected, b, c.Context)
			if err != nil {
				t.Fatalf("Unable to compare snapshot to test output: %s", err)
			}
			// check if diff is expected
			if c.ignore != nil {
				m := c.ignore.FindStringIndex(diff)
				if m != nil {
					return
				}
			}
			t.Fatalf("Snapshot test failed for: %s.  Diff:\n\n%s", t.Name(), diff)
		default:
			t.Fatalf("Snapshot test failed for: %s.  Diff: (undiffable binary format)", t.Name())
		}
	}
}

func isUpdateable() bool {
	_, ok := os.LookupEnv("UPDATE_SNAPSHOTS")
	return ok
}

func createSnapshot(testname string, b []byte, dir string, ext string) error {
	snapFile := getSnapFilename(testname, ext)
	f, err := os.Create(path.Join(dir, snapFile))
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		return err
	}
	return f.Close()
}

func readSnapshot(testname string, dir string, ext string) ([]byte, error) {
	return ioutil.ReadFile(path.Join(dir, getSnapFilename(testname, ext)))
}

func getSnapFilename(testname string, ext string) string {
	r := strings.NewReplacer("'", "-", " ", "-", "<", "-", ">", "-", "&", "-", "#", "-", "/", "-", "\\", "-")
	return r.Replace(strings.ToLower(testname)) + ext
}

func getDiff(expected []byte, b []byte, context int) (string, error) {
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(expected)),
		B:        difflib.SplitLines(string(b)),
		FromFile: "Expected",
		ToFile:   "Received",
		Context:  context,
	}
	return difflib.GetUnifiedDiffString(diff)
}
