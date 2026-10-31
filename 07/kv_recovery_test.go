package byodb07

import (
	"path/filepath"
	"testing"

	is "github.com/stretchr/testify/require"
)

func TestKVFsyncRecoveryErr(t *testing.T) {
	db := KV{Path: filepath.Join(t.TempDir(), "recovery.db"), Fsync: nofsync}
	is.NoError(t, db.Open())
	defer db.Close()
	is.NoError(t, db.Set([]byte("k"), []byte("committed")))

	// Fail after writing the new meta page, leaving its on-disk state uncertain.
	db.Fsync = fsyncErr(0, 1)
	is.Error(t, db.Set([]byte("k"), []byte("uncommitted")))
	value, ok := db.Get([]byte("k"))
	is.True(t, ok)
	is.Equal(t, "committed", string(value))

	// The next update must roll back even if repairing the old meta page fails.
	db.Fsync = fsyncErr(1)
	is.Error(t, db.Set([]byte("k"), []byte("retry")))
	value, ok = db.Get([]byte("k"))
	is.True(t, ok)
	is.Equal(t, "committed", string(value))

	// Recovery must still allow a later commit that survives reopening.
	db.Fsync = nofsync
	is.NoError(t, db.Set([]byte("k"), []byte("recovered")))
	db.Close()
	db = KV{Path: db.Path, Fsync: nofsync}
	is.NoError(t, db.Open())
	value, ok = db.Get([]byte("k"))
	is.True(t, ok)
	is.Equal(t, "recovered", string(value))
}
