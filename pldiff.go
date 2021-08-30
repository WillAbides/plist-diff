package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gosuri/uilive"
	"github.com/psanford/memfs"
	"howett.net/plist"
)

type fsDiff map[string]fmt.Stringer

func (f fsDiff) String() string {
	filenames := make([]string, 0, len(f))
	for filename := range f {
		filenames = append(filenames, filename)
	}
	sort.Strings(filenames)
	var s string
	for _, filename := range filenames {
		s += fmt.Sprintf("%s:\n%s\n\n", filename, f[filename])
	}
	return s
}

type differ struct {
	IgnorePermissionError bool
	IgnoreTimestamps      bool
}

func (d *differ) diff(a, b string) (bool, fsDiff, error) {
	fsA, err := getFS(a)
	if err != nil {
		return false, nil, err
	}
	fsB, err := getFS(b)
	if err != nil {
		return false, nil, err
	}
	return d.diffFS(fsA, fsB)
}

func (d *differ) watch(a string, stdout io.Writer) error {
	ticker := time.Tick(2 * time.Second)
	fsA, err := getFS(a)
	if err != nil {
		return err
	}
	snap, err := d.plSnapshot(fsA)
	if err != nil {
		return err
	}
	writer := uilive.New()
	writer.Out = stdout
	writer.RefreshInterval = time.Second
	writer.Start()
	defer writer.Stop()
	i := 0
	for {
		i++
		<-ticker
		fsA, err = getFS(a)
		if err != nil {
			return err
		}
		_, diff, err := d.diffFS(snap, fsA)
		if err != nil {
			return err
		}
		fmt.Fprintln(writer, diff)
	}
}

func getFS(path string) (fs.FS, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if stat.IsDir() {
		return os.DirFS(path), nil
	}
	if !stat.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is neither a director nor regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	val := memfs.New()

	err = val.WriteFile("single-file.plist", data, stat.Mode())
	if err != nil {
		return nil, err
	}
	return val, nil
}

func (d *differ) diffFS(a, b fs.FS) (bool, fsDiff, error) {
	delta := map[string]fmt.Stringer{}

	aFiles, err := getPlistFiles(a)
	if err != nil {
		return false, nil, err
	}

	for filename := range aFiles {
		var df fmt.Stringer
		df, err = d.diffFSFilename(a, b, filename)
		if err != nil {
			return false, nil, err
		}
		if df != nil {
			delta[filename] = df
		}
	}

	bFiles, err := getPlistFiles(b)
	if err != nil {
		return false, nil, err
	}

	for filename := range bFiles {
		if _, ok := aFiles[filename]; ok {
			continue
		}
		d, err := d.diffFSFilename(a, b, filename)
		if err != nil {
			return false, nil, err
		}
		if d != nil {
			delta[filename] = d
		}
	}

	return len(delta) == 0, delta, nil
}

func (d *differ) readFile(fsys fs.FS, filename string) ([]byte, error) {
	data, err := fs.ReadFile(fsys, filename)
	if errors.Is(err, os.ErrNotExist) {
		return []byte{}, nil
	}
	if errors.Is(err, os.ErrPermission) && d.IgnorePermissionError {
		return []byte{}, nil
	}
	return data, err
}

func (d *differ) diffFSFilename(a, b fs.FS, filename string) (fmt.Stringer, error) {
	bData, err := d.readFile(b, filename)
	if err != nil {
		return nil, err
	}

	aData, err := d.readFile(a, filename)
	if err != nil {
		return nil, err
	}

	var opts []cmp.Option
	if d.IgnoreTimestamps {
		opts = append(opts, cmpopts.IgnoreTypes(time.Time{}))
	}
	eq, delta, err := diffPlists(aData, bData, opts...)
	if err != nil {
		return nil, err
	}
	if eq {
		return nil, nil
	}
	return stringDiff(delta), nil
}

func getPlistFiles(fSys fs.FS) (map[string]struct{}, error) {
	files := map[string]struct{}{}
	err := fs.WalkDir(fSys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if !strings.HasSuffix(path, ".plist") {
			return nil
		}
		files[path] = struct{}{}
		return nil
	})
	return files, err
}

type stringDiff string

func (s stringDiff) String() string {
	return string(s)
}

// FileDiff is one difference between two plists
type FileDiff struct {
	path string
	old  interface{}
	new  interface{}
}

// Path is the cmp.Path pointing to this diff
func (d *FileDiff) Path() string {
	return d.path
}

// Old is the old value
func (d *FileDiff) Old() interface{} {
	return d.old
}

// New is the new value
func (d *FileDiff) New() interface{} {
	return d.new
}

func (d *FileDiff) String() string {
	var s string
	if d.old != nil {
		s += fmt.Sprintf("\t-%s: %+v (%T)\n", d.path, d.old, d.old)
	}
	if d.new != nil {
		s += fmt.Sprintf("\t+%s: %+v (%T)\n", d.path, d.new, d.new)
	}
	return s
}

func decodePlist(data []byte) (interface{}, error) {
	decoder := plist.NewDecoder(bytes.NewReader(data))
	var got interface{}
	err := decoder.Decode(&got)
	if err != nil {
		return nil, err
	}
	return got, nil
}

func diffPlists(oldData, newData []byte, opts ...cmp.Option) (eq bool, delta string, err error) {
	oldList, err := decodePlist(oldData)
	if err != nil {
		oldList = nil
	}
	newList, err := decodePlist(newData)
	if err != nil {
		newList = nil
	}
	var r diffReporter
	eq = cmp.Equal(oldList, newList, append(opts, cmp.Reporter(&r))...)
	if eq {
		return true, "", nil
	}
	return false, r.String(), nil
}

type diffReporter struct {
	path  cmp.Path
	diffs []FileDiff
}

func (r *diffReporter) PushStep(ps cmp.PathStep) {
	r.path = append(r.path, ps)
}

func (r *diffReporter) PopStep() {
	r.path = r.path[:len(r.path)-1]
}

func (r *diffReporter) Report(rs cmp.Result) {
	if rs.Equal() {
		return
	}
	diff := FileDiff{
		path: simplePathString(r.path),
	}
	vx, vy := r.path.Last().Values()
	if vx.Kind() != reflect.Invalid {
		diff.old = vx.Interface()
	}
	if vy.Kind() != reflect.Invalid {
		diff.new = vy.Interface()
	}

	r.diffs = append(r.diffs, diff)
}

func (r *diffReporter) String() string {
	result := ""
	for _, diff := range r.diffs {
		result += diff.String() + "\n"
	}
	return strings.TrimRight(result, "\n")
}

func simplePathString(pa cmp.Path) string {
	var ssPre, ssPost []string
	var numIndirect int
	for i, s := range pa {
		var nextStep cmp.PathStep
		if i+1 < len(pa) {
			nextStep = pa[i+1]
		}
		switch s := s.(type) {
		case cmp.Indirect:
			numIndirect++
			pPre, pPost := "(", ")"
			switch nextStep.(type) {
			case cmp.Indirect:
				continue // Next step is indirection, so let them batch up
			case cmp.StructField:
				numIndirect-- // Automatic indirection on struct fields
			case nil:
				pPre, pPost = "", "" // Last step; no need for parenthesis
			}
			if numIndirect > 0 {
				ssPre = append(ssPre, pPre+strings.Repeat("*", numIndirect))
				ssPost = append(ssPost, pPost)
			}
			numIndirect = 0
			continue
		case cmp.Transform:
			ssPre = append(ssPre, s.Name()+"(")
			ssPost = append(ssPost, ")")
			continue
		case cmp.TypeAssertion:
			continue
		}
		ssPost = append(ssPost, s.String())
	}
	for i, j := 0, len(ssPre)-1; i < j; i, j = i+1, j-1 {
		ssPre[i], ssPre[j] = ssPre[j], ssPre[i]
	}
	return strings.Join(ssPre, "") + strings.Join(ssPost, "")
}

func (d *differ) plSnapshot(src fs.FS) (*memfs.FS, error) {
	dest := memfs.New()
	err := fs.WalkDir(src, ".", func(path string, dir fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if dir.IsDir() {
			return dest.MkdirAll(path, dir.Type())
		}
		if !strings.HasSuffix(path, ".plist") {
			return nil
		}
		if !dir.Type().IsRegular() {
			return nil
		}

		content, err := d.readFile(src, path)
		if err != nil {
			return err
		}
		err = dest.WriteFile(path, content, dir.Type())
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return dest, nil
}
