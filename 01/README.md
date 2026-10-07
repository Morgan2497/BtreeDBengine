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

## Append-only logs: save changes without overwriting old data

Instead of replacing a whole file for every change, a program can append each operation to
the end of a log. For example:

```text
SET a = 1
SET b = 2
SET a = 3
DEL b
```

To find the current value of a key, replay its operations in order. Here, the final state is
`a = 3`; `b` has been deleted. Because earlier bytes are not overwritten, a crash during a
new append usually leaves the earlier entries intact. This makes logs useful for incremental
updates and recovery.

A log alone is not a complete database. To find a key, a simple implementation must scan the
log, which gets slower as it grows. Deleted or superseded entries also remain in the file, so
the log needs a way to reclaim space. An index can make lookups faster, and compaction can
rewrite only the current state into a smaller file.

## Checksums: detect an incomplete log entry

An append can stop partway through if the program or machine fails. For example, the file
could end halfway through a `SET` record. A log record can include its length and a checksum
computed from its contents:

```text
[record length | operation and data | checksum]
```

During recovery, the database reads a complete record and recomputes its checksum. If the
stored and computed checksums match, the record appears complete and can be replayed. If the
file ends before the record is complete, or the checksums differ, recovery rejects that
incomplete tail entry and stops at the last valid entry.

Typical failure cases for the last append are:

1. **No bytes were appended.** Recovery sees the previous complete log and keeps its state.
2. **Only part of the record was appended.** The declared length says bytes are missing, or
   the checksum is missing; recovery ignores the incomplete tail.
3. **The record bytes were written but some are damaged or missing.** Recomputing the
   checksum detects the mismatch, so recovery does not apply that record.

A checksum helps recovery distinguish a complete record from a torn write. It detects
accidental corruption; it does not repair damaged bytes, prevent corruption, or prove that
the data reached durable storage. Checksums can also collide, though a suitable checksum
makes accidental undetected damage unlikely. This simple tail-recovery approach assumes the
problem is an incomplete final append. Corruption in the middle of a log may make later
entries unusable too.

## `fsync`, the OS page cache, and durability

When a program writes a file, the operating system commonly accepts the bytes into the **page
cache** first. The page cache is RAM that the OS uses to buffer file data and speed up reads
and writes. A subsequent read may see those new bytes immediately, even though the OS has not
yet finished writing them to the storage device.

`fsync` (called `fp.Sync()` in Go) asks the OS to flush a file's pending data and required
metadata to stable storage. It is the durability step: after a successful sync, the program
has stronger grounds to treat those file contents as persisted. It is different from
atomicity. A write can be visible but not durable, and a durable file write can still need an
atomic update protocol so readers do not see a partial replacement.

This creates an important gotcha: **if `Sync` returns an error, reading the file afterward
does not tell you whether the update is durable.** The read may be served from the page cache
and show the new bytes even though the flush failed. The update's persistence is uncertain;
the error does not mean the OS rolled it back. Filesystems and devices can differ in their
failure behavior.

For `SaveData2`, a sync error happens before the rename, so the target name still points to
the old file and the function tries to remove the temporary file. For a log, a sync error
means the application should not report the append as durably committed; on recovery, it
must validate whatever complete records are actually present. In either design, do not infer
durability just because a read returns the new data.

There is a second `fsync` gotcha with filenames. Syncing a file flushes its contents, but a
rename or creation also changes the **parent directory**, which stores the mapping from a
filename to a file. To make that name change durable across power loss on systems that require
it, sync the parent directory after the rename. Thus a robust replacement sequence is:

```text
write temporary file -> sync temporary file -> rename -> sync parent directory
```

### Key terms

- **Page:** a fixed-size block used to move or cache file data; the OS page cache holds file
  data in memory pages.
- **Page cache:** the OS's in-memory buffer for file contents. It can make recent writes
  visible before they are durable.
- **Append-only log:** a file where new records are added at the end and old records are not
  overwritten.
- **Torn write:** an interrupted write that leaves only part of an intended record or page.
- **Checksum:** a value computed from data and stored with it, used later to detect accidental
  changes or incomplete writes.
- **Atomicity:** an operation appears to happen as a whole or not at all to observers or
  recovery logic.
- **Durability:** once an update is acknowledged as committed, it survives a crash or power
  loss, subject to the guarantees of the OS, filesystem, and storage device.

## The database problem

Safe replacement and append-only logs address some update failures, but a database needs more
than either technique alone. A useful database also needs:

1. **Durability and crash recovery**, so committed data can be recovered after a failure.
2. **A B+ tree key-value store**, so data can be found and updated without rewriting one large
   file.
3. **Space reuse**, so deleted or replaced data does not make storage grow forever.
4. **Tables, indexes, and transactions**, to support relational data and coordinated changes.
5. **A query language**, so applications can work with the database at a higher level.

The goal is to combine safe updates and crash recovery with fast indexes, space reuse, and
coordination for transactions and concurrent access.
