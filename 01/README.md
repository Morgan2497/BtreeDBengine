# From Files to Databases

Saving data is easy; updating it safely is harder. These two functions show how a plain file
can be replaced, and why a database needs stronger tools for safe updates, recovery, and
concurrent access.

## `SaveData1`: overwrite the target file

```go
func SaveData1(path string, data []byte) error {
	fp, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0664)
	if err != nil {
		return err
	}
	defer fp.Close()

	_, err = fp.Write(data)
	if err != nil {
		return err
	}
	return fp.Sync() // fsync
}
```

`SaveData1` opens or creates the target, truncates it to zero bytes, writes all the supplied
data, then calls `Sync` to ask the operating system to flush the file's data to storage. It
returns an error if opening, writing, or syncing fails.

### Why this approach is unsafe and limited

- **An interrupted update can destroy the old contents.** Truncation happens before the new
  data is written. If the program or machine fails during the write, the target may be empty
  or only partly written.
- **It rewrites the whole file.** The caller needs to have the replacement data available and
  the method is practical only for small files. It is inefficient for larger, frequently
  updated data.
- **It does not coordinate concurrent access.** Readers can observe an update in progress,
  and simultaneous writers can conflict. A database needs a way to control access and keep
  operations consistent.

`Sync` helps make the file contents durable, but it does not make the truncate-and-write
sequence atomic. Durability and atomic replacement are separate concerns.

## `SaveData2`: write a temporary file, then rename it

```go
func SaveData2(path string, data []byte) error {
	tmp := fmt.Sprintf("%s.tmp.%d", path, randomInt())
	fp, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0664)
	if err != nil {
		return err
	}
	defer func() { // discard the temporary file if it still exists
		fp.Close() // not expected to fail
		if err != nil {
			os.Remove(tmp)
		}
	}()

	if _, err = fp.Write(data); err != nil {
		return err
	}
	if err = fp.Sync(); err != nil {
		return err
	}
	err = os.Rename(tmp, path)
	return err
}
```

This function needs the `fmt` and `os` packages and a `randomInt()` helper that generates a
suffix for the temporary filename. It creates a temporary file beside the target, writes and
syncs the new contents, then renames the temporary file over the target. If an error occurs after opening the temporary file, the
deferred cleanup closes it and tries to remove it. The old target is left in place if writing
or syncing fails.

On filesystems that support atomic rename, a reader opening the target sees either the old
file or the new one, rather than a half-written replacement. A reader that already opened the
old file can continue using it. The rename is atomic with respect to readers, but this code
does not sync the parent directory; the rename itself is therefore not guaranteed to survive
a sudden power loss. On systems that require it, sync the parent directory after the rename
to make the name change durable.

## The database problem

`SaveData2` improves whole-file replacement, but a database needs more than replacing one
file safely. A useful database also needs:

1. **Durability and crash recovery**, so committed data can be recovered after a failure.
2. **A B+ tree key-value store**, so data can be found and updated without rewriting one large
   file.
3. **Space reuse**, so deleted or replaced data does not make storage grow forever.
4. **Tables, indexes, and transactions**, to support relational data and coordinated changes.
5. **A query language**, so applications can work with the database at a higher level.

The goal is to move from fragile whole-file updates to a database that can update, find, and
recover data safely while coordinating transactions and concurrent access.
