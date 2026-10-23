# Chapter 06: An append-only key-value store

Chapters 04 and 05 built a B+tree that knows how to find, insert, and delete keys. This
chapter gives that tree a file. The tree still decides *which nodes* must change; `KV`
decides *where their pages live*, how to write them, and which root a reopened database
should use. Start with [kv.go](./kv.go) for the file layer and [btree.go](./btree.go) for the
tree layer.

The most important distinction is between **a node**, **a page number**, and **file
bytes**. A node is a 4,096-byte `BNode`. A page number such as `7` identifies its place
in the database file. The byte offset of page 7 is `7 * 4096 = 28672`. A page number
is not a Go pointer or a byte offset.

## Follow the calls first

~~~text
Open
 ├─ createFileSync          open/create the file and sync its directory
 ├─ extendMmap             make existing file pages readable
 └─ readRoot
     └─ loadMeta           restore root and page count from page 0

Get
 └─ BTree.Get
     └─ tree.get → pageRead → mmap.chunks → file page

Set / Del
 ├─ saveMeta               remember the old root and page count
 ├─ BTree.Insert / Delete
 │   ├─ tree.get → pageRead
 │   ├─ tree.new → pageAppend → page.temp
 │   └─ tree.del → no-op in this append-only chapter
 └─ updateOrRevert
     └─ updateFile
         ├─ writePages
         │   ├─ extendMmap
         │   └─ unix.Pwritev  write page.temp to file pages
         ├─ Fsync            finish writing new nodes
         ├─ updateRoot
         │   ├─ saveMeta     encode the NEW root and page count
         │   └─ syscall.Pwrite at file offset 0
         └─ Fsync            finish writing the new metadata

Close → Munmap each chunk → close the file descriptor
~~~

Each arrow means one function calls the next. For example, `pageAppend` **queues** a
node; `writePages` **writes** queued nodes; `updateRoot` **publishes the new root page
number** in the file's metadata.

## The two layouts you must keep separate

The file is divided into database pages of `BTREE_PAGE_SIZE = 4096` bytes:

~~~text
File byte positions       Database page       Contents
[0:4096)                         0             meta page
[4096:8192)                      1             B+tree node
[8192:12288)                     2             B+tree node
...                              ...            ...
[p*4096:(p+1)*4096)              p             B+tree node
~~~

Page 0 is reserved. Its first 32 bytes have this layout:

~~~text
| signature          | root_ptr                 | page_used                |
| bytes 0–15: 16 B   | bytes 16–23: uint64, 8 B | bytes 24–31: uint64, 8 B |
~~~

`root_ptr` is the page number of the current B+tree root. `page_used` is the
`db.page.flushed` count saved in the file. If `root_ptr = 7` and `page_used = 10`, the
root is page 7, pages 0 through 9 are part of the saved database, and page 10 is the
next page number available for an append. Bytes 32–4095 of page 0 are unused here.

Every other page contains one B+tree node, whose Chapter 04 layout is:

~~~text
| type | nkeys | child pointers | offsets | encoded key-value pairs | unused |
~~~

The metadata layout describes the **whole file**. The node layout describes **one
data page**. A leaf node can contain several key-value pairs; one insertion can also
produce several new nodes when copy-on-write replaces a leaf and its ancestors.

Here is the part of the actual `KV` struct that connects these layouts:

~~~text
KV {
    Path:   "/tmp/example.db"      // file name
    fd:     7                      // example open-file descriptor
    Fsync:  syscall.Fsync          // function used to sync the file
    tree:   { root: 7 }            // page number, not byte position

    mmap: {
        total:  64 MiB             // byte range mapped for reading
        chunks: [[0] → []byte]     // each element is one mapping
    }

    page: {
        flushed: 10                // pages accounted for before temp
        temp:    []                // new nodes waiting for writePages
    }

    failed: false                  // whether a previous update failed
}
~~~

These quantities use different units:

| Expression | Unit | Meaning |
|---|---|---|
| `db.tree.root` | page number | Where tree traversal starts. |
| `db.page.flushed` | number of pages | Boundary between pages already accounted for and new pages in `temp`. It does not by itself prove that an `fsync` occurred. |
| `len(db.page.temp)` | number of nodes/pages | How many new 4,096-byte nodes are queued. |
| `db.mmap.total` | bytes | How much of the file's address range is mapped. It can exceed the file's actual size. |
| `fileSize` | bytes | Actual file length reported by `Fstat`. |

## How the tree obtains and allocates pages

`BTree` has three callbacks. `Open` connects `tree.get` to `db.pageRead`,
`tree.new` to `db.pageAppend`, and `tree.del` to an empty function. The tree can
therefore work with page numbers without knowing about `mmap`, file descriptors, or
`Pwritev`.

### `pageRead`: page number → existing node bytes

`pageRead(ptr)` searches `db.mmap.chunks` for the mapped range containing page
number `ptr` and returns a 4,096-byte slice. It does not copy that page. `chunks` is
a `[][]byte`: the outer slice holds separate mappings, while each inner `[]byte` is
a flat byte range containing one or more pages.

For a small *illustrative* mapping, suppose the first chunk holds three pages and the
second holds two. The real `extendMmap` starts with at least 64 MiB; these small
chunks make the indexing visible:

~~~text
mmap.chunks = [
    [0] → []byte length 3*4096: global pages 0, 1, 2
    [1] → []byte length 2*4096: global pages 3, 4
]

pageRead(4):
    start = 0
    chunk[0]: end = 0 + 3 = 3; 4 < 3 is false; start = 3
    chunk[1]: end = 3 + 2 = 5; 4 < 5 is true
    local byte offset = 4096 * (4 - 3) = 4096
    return chunks[1][4096:8192]  // exactly global page 4
~~~

`start` and `end` count **pages**; `offset` counts **bytes inside a chunk**. In
normal use, page 0 is metadata, so tree child pointers refer to data pages 1 and up.

### `pageAppend`: new node bytes → future page number

The B+tree calls `tree.new(node)` when it has built a replacement node. That invokes
`pageAppend(node)`. Suppose `flushed = 10` and `temp` already holds one node:

~~~text
Before:
page: { flushed: 10, temp: [[0] → nodeA, future page 10] }

ptr = flushed + len(temp) = 10 + 1 = 11
temp = append(temp, nodeB)

After:
page: {
    flushed: 10
    temp: [
        [0] → nodeA, page 10
        [1] → nodeB, page 11
    ]
}
return 11
~~~

`append` adds **one `[]byte` node** to the outer `[][]byte` list; it does not
write 4,096 individual list entries. The B+tree can immediately put returned page
number 11 into a new parent node. `page.flushed` stays 10 until `writePages` runs.
The current `pageAppend` assumes each supplied node is exactly one database page.

## How pages move from `temp` to the file

`writePages` handles every node waiting in `page.temp`. Continue with
`flushed = 10` and two pending 4,096-byte nodes:

~~~text
Before writePages:
page: { flushed: 10, temp: [nodeA → page 10, nodeB → page 11] }
file: pages 0–9 already exist
~~~

First it calculates the byte position **after** both new pages:

~~~text
size = (flushed + len(temp)) * BTREE_PAGE_SIZE
     = (10 + 2) * 4096
     = 49,152 bytes
~~~

`size` is the required end position, **not** the number of bytes being written.
`extendMmap(db, 49152)` makes sure that byte range can be read through `mmap`.
Then `writePages` calculates the starting byte position for the write:

~~~text
offset = flushed * BTREE_PAGE_SIZE
       = 10 * 4096
       = 40,960 bytes
~~~

`unix.Pwritev(db.fd, db.page.temp, offset)` writes the two input slices
consecutively at that explicit file offset:

~~~text
Existing pages: [0:40960)             pages 0–9
nodeA:          [40960:45056)         page 10, 4096 bytes
nodeB:          [45056:49152)         page 11, 4096 bytes
                                       ↑ next free byte position

Actual bytes written = 4096 + 4096 = 8192.
~~~

On success, `writePages` increments `flushed` from 10 to 12, changes `temp` to
`temp[:0]` (length zero), and returns. `temp[:0]` empties the pending *list*; it
does not erase the node bytes. The subsequent `fsync` in `updateFile` is a separate
step that requests durable storage.

### Why `extendMmap` adds much more than one write needs

`extendMmap` manages **readable address space**, while `Pwritev` writes **actual
file bytes**. If 64 MiB are mapped and a write needs 8 KiB beyond that range,
`extendMmap` adds a 64 MiB mapping even though `Pwritev` writes only 8 KiB.
`64 << 20` means `64 * 2^20 = 67,108,864` bytes, or 64 MiB. For later growth,
`alloc` starts at the larger of 64 MiB and the current mapped total; the loop
doubles `alloc` until `total + alloc >= size`. The new `[]byte` mapping is appended
to `mmap.chunks`, and `mmap.total` increases by its byte length. Requiring a new
mapping less often avoids accumulating many tiny mappings.

The read-only `PROT_READ, MAP_SHARED` mapping is used for `pageRead`. New node
bytes are written through `Pwritev`. Mapping beyond the present file length does
not mean those future pages already contain file data.

## How the meta page identifies the tree

`saveMeta` turns the current in-memory root and page count into 32 bytes.
`loadMeta` does the reverse. With `tree.root = 11` and `page.flushed = 12`:

~~~text
byte positions    stored bytes                         interpreted as
0–15              "BuildYourOwnDB06"                    file signature
16–23             0B 00 00 00 00 00 00 00             root page 11
24–31             0C 00 00 00 00 00 00 00             12 pages used
~~~

`binary.LittleEndian.PutUint64(data[16:], 11)` writes **eight bytes** beginning at
position 16. `binary.LittleEndian.Uint64(data[16:])` reads those same eight bytes;
although `data[16:]` extends farther, `Uint64` consumes only its first eight bytes.
The same rule applies at position 24. The signature is copied into the first 16
bytes and checked by `readRoot`.

`saveMeta` only makes a `[]byte` in memory. `updateRoot` calls `saveMeta(db)`
and then `syscall.Pwrite(db.fd, metadata, 0)`. The `0` means **file byte offset
zero**, inside page 0. It changes the saved *root page number*, not the already
written root node's contents. `loadMeta` only updates the Go struct; it does not
write the file.

`readRoot` decides how to initialize the database:

- With a zero-length file, it sets `page.flushed = 1` to reserve page 0. The
  first B+tree node can then receive page number 1.
- With an existing file, it reads `mmap.chunks[0]`, calls `loadMeta`, checks the
  signature, and verifies that the file length is a multiple of 4,096. It also
  requires `0 < flushed <= fileSize/4096` and `0 < root < flushed`.

The check allows `fileSize/4096` to exceed the saved `flushed` count. A failed
append can leave trailing file pages that the saved root never points to.

## Open, read, write, reopen: one complete example

Start with a new empty file and call `db.Open()`.
`createFileSync` opens the parent directory, opens or creates the database file
with `Openat`, syncs the directory entry, and returns the file descriptor.
`Fstat` reports a file size of zero. `extendMmap(db, 0)` returns without mapping
anything. `readRoot(db, 0)` reserves page 0:

~~~text
After Open on a new file:
KV {
    tree: { root: 0 }               // no tree yet
    mmap: { total: 0, chunks: [] }
    page: { flushed: 1, temp: [] }  // page 0 reserved for metadata
}
~~~

Now call `db.Set([]byte("k1"), []byte("hi"))`:

1. `Set` calls `saveMeta` before changing the tree. The snapshot says
   `root = 0, flushed = 1`; it is available if this update must be reverted.
2. `BTree.Insert` creates a root leaf containing its dummy key and `"k1":"hi"`.
   It calls `tree.new`, which calls `pageAppend`. With `flushed = 1` and an empty
   `temp`, the new root gets page number 1. `tree.root` becomes 1 and `temp`
   contains the new 4,096-byte leaf.
3. `Set` calls `updateOrRevert`, which calls `updateFile`.
4. `writePages` computes `size = (1 + 1) * 4096 = 8192`. `extendMmap` creates
   its initial 64 MiB mapping. `Pwritev` writes the new root at file byte offset
   `1 * 4096 = 4096`, so it occupies page 1. `flushed` becomes 2 and `temp`
   becomes empty.
5. The first `Fsync` finishes the new node write. `updateRoot` then calls
   `saveMeta` *again*, now encoding **root 1, flushed 2**, and writes those 32
   bytes at file offset zero. The second `Fsync` finishes the metadata write.

~~~text
After successful Set:
file page 0: signature, root_ptr=1, page_used=2
file page 1: leaf containing the dummy key and "k1":"hi"

KV {
    tree: { root: 1 }
    page: { flushed: 2, temp: [] }
}
~~~

After `Close`, create a fresh `KV{Path: "/tmp/example.db"}` and call `Open`.
`Fstat` reports `8192` bytes, `extendMmap` maps the existing file, and
`readRoot → loadMeta` restore `root = 1` and
`flushed = 2`. `Get("k1")` calls `BTree.Get`; the tree follows root pointer 1
through `pageRead(1)` and returns `"hi"`.

### Why an update has two syncs

For an existing database, imagine the saved metadata says `root = 7,
flushed = 10`. A copy-on-write update builds new pages 10 and 11 and sets the
**in-memory** root to 11. The commit proceeds in this order:

~~~text
1. writePages writes pages 10 and 11
   disk metadata still says root 7 → old tree is the saved version
2. first Fsync confirms the new pages
3. updateRoot writes root 11 and page_used 12 into page 0
4. second Fsync confirms the new metadata
~~~

The first sync orders the new pages before the saved root can point at them.
The second sync persists the root change. Old tree pages remain in the file;
`tree.del` does nothing in this chapter, and space reuse belongs to the next
chapter. The chapter treats the small metadata write as the root switch. Its
power-loss atomicity depends on storage behavior; the supplied source itself
marks that assumption for review.

## Delete and error recovery

`Del` saves the old metadata, then calls `BTree.Delete`. If the key is absent,
it returns without writing pages. If the key is found, the tree may copy or
merge nodes, call `pageAppend` for replacements, and update its in-memory
root. `Del` then uses the same `updateOrRevert → updateFile` path as `Set`.

`updateOrRevert` is the boundary between an attempted update and a usable
in-memory tree. The `meta` argument is a **snapshot from before** the current
`Insert` or `Delete`:

~~~text
Before update: meta = { root: 7, flushed: 10 }
After tree work: db.tree.root = 11, db.page.temp = [page 10, page 11]
~~~

If `updateFile` succeeds, the new pages and new metadata are saved. If it
returns an error, `updateOrRevert` sets `db.failed = true`, calls
`loadMeta(db, meta)` to restore `root = 7` and `flushed = 10` in memory,
and empties `temp`. New pages possibly left in the file are not part of the
restored tree.

Before a **later** update, `db.failed` causes `updateOrRevert` to rewrite the
known-good old `meta` at file offset zero and sync it. Only after that repair
does it attempt the later update. `Fsync` is a function field so tests can
substitute a function that returns an error at a chosen sync point; `Open`
normally sets it to `syscall.Fsync`.

## Function reference

| Function | Called by | Job and effect |
|---|---|---|
| `createFileSync` | `Open` | Open/create the file, sync its parent directory, return the file descriptor. |
| `Open` | application | Connect B+tree callbacks, get file size, map existing bytes, restore metadata. |
| `extendMmap` | `Open`, `writePages` | Grow the mapped byte range and append a new `[]byte` to `mmap.chunks` if needed. |
| `readRoot` | `Open` | Reserve the meta page for an empty file or validate and load an existing one. |
| `loadMeta` | `readRoot`, `updateOrRevert` | Decode saved root and page count into the `KV` struct. |
| `Get` | application | Delegate lookup to `BTree.Get`. |
| `pageRead` | `BTree.get` callback | Turn a page number into a 4,096-byte slice from an mmap chunk. |
| `Set` | application | Snapshot metadata, insert in the tree, persist or revert. |
| `Del` | application | Snapshot metadata, delete if found, persist or revert. |
| `pageAppend` | `BTree.new` callback | Assign a future page number and enqueue a new node in `temp`. |
| `writePages` | `updateFile` | Write all pending nodes with `Pwritev`, advance `flushed`, clear `temp`. |
| `updateFile` | `updateOrRevert` | Write nodes, sync, write new root metadata, sync again. |
| `saveMeta` | `Set`, `Del`, `updateRoot` | Encode signature, root pointer, and page count as 32 bytes. |
| `updateRoot` | `updateFile` | Write current metadata at file byte offset zero. |
| `updateOrRevert` | `Set`, `Del` | Repair previous failed metadata if needed; commit or restore current in-memory state. |
| `Close` | application, `Open` error path | Unmap each chunk and close the database file descriptor. |

## Go and syscall notation used here

| Notation | Meaning in this chapter |
|---|---|
| `*KV` | A pointer to the same mutable `KV` object; helpers change its fields. |
| `[]byte` | One byte slice, such as one node or one mmap chunk. |
| `[][]byte` | A slice whose elements are byte slices; `temp` holds nodes, `chunks` holds mappings. |
| `data[16:]` with `Uint64` | Pass bytes starting at 16; `Uint64` reads only the first eight. |
| `64 << 20` | Left shift: `64 * 2^20 = 64 MiB`. |
| `os.O_RDWR` with `os.O_CREATE` | Combine file-opening flags with bitwise OR. |
| `0o644` | Octal file permissions used if a new file is created. |
| `syscall.Fstat(fd, &finfo)` | Ask the OS for file metadata, filling `finfo` through its address. |
| `syscall.Mmap` / `Munmap` | Map a file range into memory / release that mapping. |
| `unix.Pwritev(fd, temp, offset)` | Write several byte slices consecutively, starting at an explicit file byte offset. |
| `syscall.Pwrite(fd, meta, 0)` | Write one byte slice at the start of the file. |
| `Fsync(fd)` | Ask the OS to complete persistence of prior file writes. |
| `defer` | Schedule cleanup to run when the current function returns. |
| `goto fail` | Jump to the shared cleanup block on an `Open` error. |

## Current working-copy notes

Run `go build ./06` from the repository root to check compilation and
`go test ./06` to run the existing tests. The current local test file covers
the B+tree; it does not yet prove that `KV` survives a close and reopen. A
useful persistence check is: `Open → Set("k1","hi") → Close → Open → Get("k1")`,
then delete the key and check again after another reopen.

All Go files in this folder use `package byodb06`. The Chapter 04 folder
continues to use `package byodb04`; Go compiles each folder as its own package.
`Open` maps the existing file once and reaches the shared `Close` cleanup if
`Fstat`, `extendMmap`, or `readRoot` fails.

`Close` releases mappings and the file descriptor but does not reset the
fields of that `KV` object. Use a fresh `KV` when demonstrating a reopen.
