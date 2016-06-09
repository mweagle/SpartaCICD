package resources

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"sync"
	"time"
)

type _escLocalFS struct{}

var _escLocal _escLocalFS

type _escStaticFS struct{}

var _escStatic _escStaticFS

type _escDirectory struct {
	fs   http.FileSystem
	name string
}

type _escFile struct {
	compressed string
	size       int64
	modtime    int64
	local      string
	isDir      bool

	once sync.Once
	data []byte
	name string
}

func (_escLocalFS) Open(name string) (http.File, error) {
	f, present := _escData[path.Clean(name)]
	if !present {
		return nil, os.ErrNotExist
	}
	return os.Open(f.local)
}

func (_escStaticFS) prepare(name string) (*_escFile, error) {
	f, present := _escData[path.Clean(name)]
	if !present {
		return nil, os.ErrNotExist
	}
	var err error
	f.once.Do(func() {
		f.name = path.Base(name)
		if f.size == 0 {
			return
		}
		var gr *gzip.Reader
		b64 := base64.NewDecoder(base64.StdEncoding, bytes.NewBufferString(f.compressed))
		gr, err = gzip.NewReader(b64)
		if err != nil {
			return
		}
		f.data, err = ioutil.ReadAll(gr)
	})
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (fs _escStaticFS) Open(name string) (http.File, error) {
	f, err := fs.prepare(name)
	if err != nil {
		return nil, err
	}
	return f.File()
}

func (dir _escDirectory) Open(name string) (http.File, error) {
	return dir.fs.Open(dir.name + name)
}

func (f *_escFile) File() (http.File, error) {
	type httpFile struct {
		*bytes.Reader
		*_escFile
	}
	return &httpFile{
		Reader:   bytes.NewReader(f.data),
		_escFile: f,
	}, nil
}

func (f *_escFile) Close() error {
	return nil
}

func (f *_escFile) Readdir(count int) ([]os.FileInfo, error) {
	return nil, nil
}

func (f *_escFile) Stat() (os.FileInfo, error) {
	return f, nil
}

func (f *_escFile) Name() string {
	return f.name
}

func (f *_escFile) Size() int64 {
	return f.size
}

func (f *_escFile) Mode() os.FileMode {
	return 0
}

func (f *_escFile) ModTime() time.Time {
	return time.Unix(f.modtime, 0)
}

func (f *_escFile) IsDir() bool {
	return f.isDir
}

func (f *_escFile) Sys() interface{} {
	return f
}

// FS returns a http.Filesystem for the embedded assets. If useLocal is true,
// the filesystem's contents are instead used.
func FS(useLocal bool) http.FileSystem {
	if useLocal {
		return _escLocal
	}
	return _escStatic
}

// Dir returns a http.Filesystem for the embedded assets on a given prefix dir.
// If useLocal is true, the filesystem's contents are instead used.
func Dir(useLocal bool, name string) http.FileSystem {
	if useLocal {
		return _escDirectory{fs: _escLocal, name: name}
	}
	return _escDirectory{fs: _escStatic, name: name}
}

// FSByte returns the named file from the embedded assets. If useLocal is
// true, the filesystem's contents are instead used.
func FSByte(useLocal bool, name string) ([]byte, error) {
	if useLocal {
		f, err := _escLocal.Open(name)
		if err != nil {
			return nil, err
		}
		return ioutil.ReadAll(f)
	}
	f, err := _escStatic.prepare(name)
	if err != nil {
		return nil, err
	}
	return f.data, nil
}

// FSMustByte is the same as FSByte, but panics if name is not present.
func FSMustByte(useLocal bool, name string) []byte {
	b, err := FSByte(useLocal, name)
	if err != nil {
		panic(err)
	}
	return b
}

// FSString is the string version of FSByte.
func FSString(useLocal bool, name string) (string, error) {
	b, err := FSByte(useLocal, name)
	return string(b), err
}

// FSMustString is the string version of FSMustByte.
func FSMustString(useLocal bool, name string) string {
	return string(FSMustByte(useLocal, name))
}

var _escData = map[string]*_escFile{

	"/resources/source/userdata.sh": {
		local:   "resources/source/userdata.sh",
		size:    4978,
		modtime: 1465445693,
		compressed: `
H4sIAAAJbogA/9RY627bOhL+r6dgnQDd7ZaSnaRuY0CL9a2t0cQ2JLtFURQqLTGWEEkUSMqx2+bddyjJ
tuTYiQ9witMbGpGcGQ5nvm+GzMkzYxbExowIH+El1bqjYXc0tey+0xt9Gl6N2j1nal2ZvpSJaBnGPJB+
OtNdFhkui12WckEzA5yGlAgqDI/dxSEjnrFo6Od6HXNXf32+FXbCIE6XDom85oU2nnauBl3n/cieDNvX
ffMbdc8iKolHJEEYJ+ksDFzsMyFjEtFv2tgafGxP+s5g7LR7PWtXPmQuCXGQLC6+aaWDdNo2bNKeTt47
U7tvZTv9+IH0qaBc2UX39/ulx23b/jSyepn0mAhxx7inpO1x25q0ne6g23M6g2Hb+gyyk/em4bOIGuks
jWVqKCWb8kXg0mG+ix6SaOYRvTg8nPqd1bezo/Rt2/yBam/jVusdlW0pa6iFvqCaMtLrDGIhSexSiwoI
4sZeDb1EtX7sJSyIpd72PE6FqKGv6H5rHA427Hcng9HQsSfWYPjOrJpUQQBTreps6bD/O9111KjK9iD4
AJ+1U5p28jf/0U4Q/JtQIamHWIymWXxRo6nXLzS12r4etFAR9SAic0Chv4iwEF6RCwB2HAA0MhWchR/D
uReU47N6o1l/BQsN9C8SBbjenF1eNJvNf2vBDSTgGcI3qFbJawqaCnC68CHWmvRprCHkpjxEeCGQYgoQ
pdG81M9eXejFTyMkEg6QKeMcrgwdMqvM+RHz0H+Wj8mQROI5lShQiQhDJNJEwU0wjvBKuwkgE2hQrFE4
60r6QTzXRI7JsriQLEE/fz5hcb2aJuADhRlUmppz4lXn9hghd8INA5TG34MEQSlRVn8FXEYpR1CGEBQm
wsHxO4HEOXIT+B9SkxHzvJO6t+Dl/X0x/kBXMKjGmyQJFCAiAxbr4LKWO76buR0phL3KurbJ5emBuvFL
YtBdV9zu4CCUN1V5H5DxFTrd3w0eRGBjBx2EbklEIZNH4M2iKvLilq5eaEL4GD7mNEZYIi6Icrsip/qB
AyIID9Hz5+gIDShlt5T/NR3oZQIy6ohgHgNtysoApAPmdWhZO+BIpc948J16zlZM/JKM29Nx3/o4sEcW
DKz+29a6Fm0Z6OmMzw2FDujWMUslEhQy4wFLkBdw6koGfIlu4RvhBPJDpVtSV1m80T1Icqlhfup3nO3W
quG8NWtfEs6gJEStbeO/o7OvGlwcIhJ75gFsgAz0cegm0PZV5HC67tGnj/bzqlKy7l77ldZtHZSKJOMi
yQoSR6AAS0GyW8lD+S02M6Ft9pWoOAYa6t4DRubQy7NGgfOWj9aTkM/Tw90dtOlSqpiFWBG5QMDpzk2r
9ab+pq52otCOKnLVG1Ymp8VpBOl0hdnQNiAxDRklWsIDOIFcmZeXlxqcB3wkXJqSpzQbgr/ZRBrTZQKK
1NOyCUAdWKvnA04lD6gwzzW6DKTLPPiuvzzTVFdScSehOelb19n4jgQlZQaZnnOWJuYNCQXVboMwrM4o
/JhFHRbSA8Q7IZvfBCE1jQXhBgyMCkR1mNmRdCKynK2gfZuN687u2oy4t2lS+JOtuNAAU75fC9pwLIVD
YzILqVf4yGkeVQeEKOfFbD7YeFt1ElbWjpaEHmxZXqs6qlYOOarW9jpa06jrM1Q7fZT8NfTfA3VjJ9Jq
Uitf1IGUH/rWccUkI8zT9SQTA6CrD6yqmsESWXq6bATWlEaNs9d6Hf42isniEXKQ6VnJL2RzaxhYsYAb
0kOdLc3/eE5xxuQxjMpO/AeQKvfz9+HVXiocR638KDm7NjdOB66c9udh9xF2iQSAQ8Qqdre8OnRlRUoM
UH98fz6yKf/xxHii2WyD/LuSouThP0yHJ8H7CB9Kp8iZcILGRLo+PFURvHRKt13YC56K2Cp+g9DKf1Sf
cOp2rdADyvZmq30v6QKD6jG9950Na/8PAAD//1VdgZFyEwAA
`,
	},

	"/": {
		isDir: true,
		local: "/",
	},

	"/resources": {
		isDir: true,
		local: "/resources",
	},

	"/resources/source": {
		isDir: true,
		local: "/resources/source",
	},
}
