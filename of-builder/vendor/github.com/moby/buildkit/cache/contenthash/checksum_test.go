package contenthash

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/snapshot/naive"
	"github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/cache/metadata"
	"github.com/moby/buildkit/snapshot"
	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil"
)

const (
	dgstFileData0     = digest.Digest("sha256:cd8e75bca50f2d695f220d0cb0997d8ead387e4f926e8669a92d7f104cc9885b")
	dgstDirD0         = digest.Digest("sha256:5f8fc802a74ea165f2dfa25d356a581a3d8282a343192421b670079819f4afa7")
	dgstDirD0Modified = digest.Digest("sha256:4071993a2baf46d92cf3ea64a3fac9f7ab5d0b3876aed0333769ed99756f968b")
)

func TestChecksumBasicFile(t *testing.T) {
	tmpdir, err := ioutil.TempDir("", "buildkit-state")
	require.NoError(t, err)
	defer os.RemoveAll(tmpdir)

	cm := setupCacheManager(t, tmpdir)
	defer cm.Close()

	ch := []string{
		"ADD foo file data0",
		"ADD bar file data1",
		"ADD d0 dir",
		"ADD d0/abc file data0",
		"ADD d0/def symlink abc",
		"ADD d0/ghi symlink nosuchfile",
	}

	ref := createRef(t, cm, ch)

	// for the digest values, the actual values are not important in development
	// phase but consistency is

	cc, err := newCacheContext(ref.Metadata())
	assert.NoError(t, err)

	_, err = cc.Checksum(context.TODO(), ref, "nosuch")
	assert.Error(t, err)

	dgst, err := cc.Checksum(context.TODO(), ref, "foo")
	assert.NoError(t, err)

	assert.Equal(t, dgstFileData0, dgst)

	// second file returns different hash
	dgst, err = cc.Checksum(context.TODO(), ref, "bar")
	assert.NoError(t, err)

	assert.Equal(t, digest.Digest("sha256:c2b5e234f5f38fc5864da7def04782f82501a40d46192e4207d5b3f0c3c4732b"), dgst)

	// same file inside a directory
	dgst, err = cc.Checksum(context.TODO(), ref, "d0/abc")
	assert.NoError(t, err)

	assert.Equal(t, dgstFileData0, dgst)

	// repeat because codepath is different
	dgst, err = cc.Checksum(context.TODO(), ref, "d0/abc")
	assert.NoError(t, err)

	assert.Equal(t, dgstFileData0, dgst)

	// symlink to the same file is followed, returns same hash
	dgst, err = cc.Checksum(context.TODO(), ref, "d0/def")
	assert.NoError(t, err)

	assert.Equal(t, dgstFileData0, dgst)

	_, err = cc.Checksum(context.TODO(), ref, "d0/ghi")
	assert.Error(t, err)
	assert.Equal(t, errNotFound, errors.Cause(err))

	dgst, err = cc.Checksum(context.TODO(), ref, "/")
	assert.NoError(t, err)

	assert.Equal(t, digest.Digest("sha256:6308f8be7bb12f5f6c99635cfa09e9d7055a6c03033e0f6b034cb48849906180"), dgst)

	dgst, err = cc.Checksum(context.TODO(), ref, "d0")
	assert.NoError(t, err)

	assert.Equal(t, dgstDirD0, dgst)

	err = ref.Release(context.TODO())
	require.NoError(t, err)

	// this is same directory as previous d0
	ch = []string{
		"ADD abc file data0",
		"ADD def symlink abc",
		"ADD ghi symlink nosuchfile",
	}

	ref = createRef(t, cm, ch)

	cc, err = newCacheContext(ref.Metadata())
	assert.NoError(t, err)

	dgst, err = cc.Checksum(context.TODO(), ref, "/")
	assert.NoError(t, err)

	assert.Equal(t, dgstDirD0, dgst)

	err = ref.Release(context.TODO())
	require.NoError(t, err)

	// test that removing broken symlink changes hash even though symlink itself can't be checksummed
	ch = []string{
		"ADD abc file data0",
		"ADD def symlink abc",
	}

	ref = createRef(t, cm, ch)

	cc, err = newCacheContext(ref.Metadata())
	assert.NoError(t, err)

	dgst, err = cc.Checksum(context.TODO(), ref, "/")
	assert.NoError(t, err)

	assert.Equal(t, dgstDirD0Modified, dgst)
	assert.NotEqual(t, dgstDirD0, dgst)

	err = ref.Release(context.TODO())
	require.NoError(t, err)

	// test multiple scans, get checksum of nested file first

	ch = []string{
		"ADD abc dir",
		"ADD abc/aa dir",
		"ADD abc/aa/foo file data2",
		"ADD d0 dir",
		"ADD d0/abc file data0",
		"ADD d0/def symlink abc",
		"ADD d0/ghi symlink nosuchfile",
	}

	ref = createRef(t, cm, ch)

	cc, err = newCacheContext(ref.Metadata())
	assert.NoError(t, err)

	dgst, err = cc.Checksum(context.TODO(), ref, "abc/aa/foo")
	assert.NoError(t, err)

	assert.Equal(t, digest.Digest("sha256:1c67653c3cf95b12a0014e2c4cd1d776b474b3218aee54155d6ae27b9b999c54"), dgst)
	assert.NotEqual(t, dgstDirD0, dgst)

	// this will force rescan
	dgst, err = cc.Checksum(context.TODO(), ref, "d0")
	assert.NoError(t, err)

	assert.Equal(t, dgstDirD0, dgst)

	err = ref.Release(context.TODO())
	require.NoError(t, err)
}

func TestHandleChange(t *testing.T) {
	tmpdir, err := ioutil.TempDir("", "buildkit-state")
	require.NoError(t, err)
	defer os.RemoveAll(tmpdir)

	cm := setupCacheManager(t, tmpdir)
	defer cm.Close()

	ch := []string{
		"ADD foo file data0",
		"ADD bar file data1",
		"ADD d0 dir",
		"ADD d0/abc file data0",
		"ADD d0/def symlink abc",
		"ADD d0/ghi symlink nosuchfile",
	}

	ref := createRef(t, cm, nil)

	// for the digest values, the actual values are not important in development
	// phase but consistency is

	cc, err := newCacheContext(ref.Metadata())
	assert.NoError(t, err)

	err = emit(cc.HandleChange, changeStream(ch))
	assert.NoError(t, err)

	dgstFoo, err := cc.Checksum(context.TODO(), ref, "foo")
	assert.NoError(t, err)

	assert.Equal(t, dgstFileData0, dgstFoo)

	// symlink to the same file is followed, returns same hash
	dgst, err := cc.Checksum(context.TODO(), ref, "d0/def")
	assert.NoError(t, err)

	assert.Equal(t, dgstFoo, dgst)

	// symlink to the same file is followed, returns same hash
	dgst, err = cc.Checksum(context.TODO(), ref, "d0")
	assert.NoError(t, err)

	assert.Equal(t, dgstDirD0, dgst)

	ch = []string{
		"DEL d0/ghi file",
	}

	err = emit(cc.HandleChange, changeStream(ch))
	assert.NoError(t, err)

	dgst, err = cc.Checksum(context.TODO(), ref, "d0")
	assert.NoError(t, err)
	assert.Equal(t, dgstDirD0Modified, dgst)

	ch = []string{
		"DEL d0 dir",
	}

	err = emit(cc.HandleChange, changeStream(ch))
	assert.NoError(t, err)

	_, err = cc.Checksum(context.TODO(), ref, "d0")
	assert.Error(t, err)
	assert.Equal(t, errNotFound, errors.Cause(err))

	_, err = cc.Checksum(context.TODO(), ref, "d0/abc")
	assert.Error(t, err)
	assert.Equal(t, errNotFound, errors.Cause(err))

	err = ref.Release(context.TODO())
	require.NoError(t, err)
}

func TestPersistence(t *testing.T) {
	tmpdir, err := ioutil.TempDir("", "buildkit-state")
	require.NoError(t, err)
	defer os.RemoveAll(tmpdir)

	cm := setupCacheManager(t, tmpdir)
	defer cm.Close()

	ch := []string{
		"ADD foo file data0",
		"ADD bar file data1",
		"ADD d0 dir",
		"ADD d0/abc file data0",
		"ADD d0/def symlink abc",
		"ADD d0/ghi symlink nosuchfile",
	}

	ref := createRef(t, cm, ch)
	id := ref.ID()

	dgst, err := Checksum(context.TODO(), ref, "foo")
	assert.NoError(t, err)
	assert.Equal(t, dgstFileData0, dgst)

	err = ref.Release(context.TODO())
	require.NoError(t, err)

	ref, err = cm.Get(context.TODO(), id)
	assert.NoError(t, err)

	dgst, err = Checksum(context.TODO(), ref, "foo")
	assert.NoError(t, err)
	assert.Equal(t, dgstFileData0, dgst)

	err = ref.Release(context.TODO())
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond) // saving happens on the background

	cm.Close()
	getDefaultManager().lru.Purge()
	cm = setupCacheManager(t, tmpdir)
	defer cm.Close()

	ref, err = cm.Get(context.TODO(), id)
	assert.NoError(t, err)

	dgst, err = Checksum(context.TODO(), ref, "foo")
	assert.NoError(t, err)
	assert.Equal(t, dgstFileData0, dgst)
}

func createRef(t *testing.T, cm cache.Manager, files []string) cache.ImmutableRef {
	mref, err := cm.New(context.TODO(), nil, cache.CachePolicyRetain)
	require.NoError(t, err)

	mounts, err := mref.Mount(context.TODO(), false)
	require.NoError(t, err)

	lm := snapshot.LocalMounter(mounts)

	mp, err := lm.Mount()
	require.NoError(t, err)

	err = writeChanges(mp, changeStream(files))
	lm.Unmount()
	require.NoError(t, err)

	ref, err := mref.Commit(context.TODO())
	require.NoError(t, err)

	return ref
}

func setupCacheManager(t *testing.T, tmpdir string) cache.Manager {
	snapshotter, err := naive.NewSnapshotter(filepath.Join(tmpdir, "snapshots"))
	require.NoError(t, err)

	md, err := metadata.NewStore(filepath.Join(tmpdir, "metadata.db"))
	require.NoError(t, err)

	cm, err := cache.NewManager(cache.ManagerOpt{
		Snapshotter:   snapshotter,
		MetadataStore: md,
	})
	require.NoError(t, err)

	return cm
}

// these test helpers are from tonistiigi/fsutil

type change struct {
	kind fsutil.ChangeKind
	path string
	fi   os.FileInfo
	data string
}

func changeStream(dt []string) (changes []*change) {
	for _, s := range dt {
		changes = append(changes, parseChange(s))
	}
	return
}

func parseChange(str string) *change {
	f := strings.Fields(str)
	errStr := fmt.Sprintf("invalid change %q", str)
	if len(f) < 3 {
		panic(errStr)
	}
	c := &change{}
	switch f[0] {
	case "ADD":
		c.kind = fsutil.ChangeKindAdd
	case "CHG":
		c.kind = fsutil.ChangeKindModify
	case "DEL":
		c.kind = fsutil.ChangeKindDelete
	default:
		panic(errStr)
	}
	c.path = f[1]
	st := &fsutil.Stat{}
	switch f[2] {
	case "file":
		if len(f) > 3 {
			if f[3][0] == '>' {
				st.Linkname = f[3][1:]
			} else {
				c.data = f[3]
				st.Size_ = int64(len(f[3]))
			}
		}
		st.Mode |= 0644
	case "dir":
		st.Mode |= uint32(os.ModeDir)
		st.Mode |= 0700
	case "symlink":
		if len(f) < 4 {
			panic(errStr)
		}
		st.Mode |= uint32(os.ModeSymlink)
		st.Linkname = f[3]
		st.Mode |= 0777
	}

	c.fi = &fsutil.StatInfo{st}
	return c
}

func emit(fn fsutil.HandleChangeFn, inp []*change) error {
	for _, c := range inp {
		stat, ok := c.fi.Sys().(*fsutil.Stat)
		if !ok {
			return errors.Errorf("invalid non-stat change %s", c.fi.Name())
		}
		fi := c.fi
		if c.kind != fsutil.ChangeKindDelete {
			h, err := NewFromStat(stat)
			if err != nil {
				return err
			}
			if _, err := io.Copy(h, strings.NewReader(c.data)); err != nil {
				return err
			}
			fi = &withHash{FileInfo: c.fi, digest: digest.NewDigest(digest.SHA256, h)}
		}
		if err := fn(c.kind, c.path, fi, nil); err != nil {
			return err
		}
	}
	return nil
}

type withHash struct {
	digest digest.Digest
	os.FileInfo
}

func (wh *withHash) Digest() digest.Digest {
	return wh.digest
}

func writeChanges(p string, inp []*change) error {
	for _, c := range inp {
		if c.kind == fsutil.ChangeKindAdd {
			p := filepath.Join(p, c.path)
			stat, ok := c.fi.Sys().(*fsutil.Stat)
			if !ok {
				return errors.Errorf("invalid non-stat change %s", p)
			}
			if c.fi.IsDir() {
				if err := os.Mkdir(p, 0700); err != nil {
					return err
				}
			} else if c.fi.Mode()&os.ModeSymlink != 0 {
				if err := os.Symlink(stat.Linkname, p); err != nil {
					return err
				}
			} else if len(stat.Linkname) > 0 {
				if err := os.Link(filepath.Join(p, stat.Linkname), p); err != nil {
					return err
				}
			} else {
				f, err := os.Create(p)
				if err != nil {
					return err
				}
				if len(c.data) > 0 {
					if _, err := f.Write([]byte(c.data)); err != nil {
						return err
					}
				}
				f.Close()
			}
		}
	}
	return nil
}
