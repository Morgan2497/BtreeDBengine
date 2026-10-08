# Indexing Data Structures

An index organizes keys so a database can find records without scanning every record. The
right structure depends on the queries the database must answer and on the cost of changing
the index.

## Three kinds of queries

1. **Full scan:** examine every record. This is useful when most records are needed, but its
   work grows with the data set: `O(N)`.
2. **Point lookup:** find one key, such as `user:42`.
3. **Range lookup:** find keys between two boundaries, such as all keys from `user:20` through
   `user:50`.

An ordered index supports both point and range lookups. A range lookup has two steps: seek to
the first matching key, then walk through nearby keys in order. A point lookup only needs the
seek.

## Hash tables

A hash table is a good fit for point lookups: it can find a key without keeping keys sorted.
That makes it a poor fit for range queries, where the database needs keys in order. A hash
table also has to grow as it fills; moving all entries at once can require `O(N)` work.

## Sorted arrays

Binary search finds a key in a sorted array in `O(log N)` comparisons. For variable-size keys,
an index can keep offsets or pointers into the stored data and compare keys through them.

The problem is insertion and deletion. To insert into the middle, later entries must shift;
deleting an entry also leaves a gap to close. Either update can take `O(N)` work. Sorted arrays
are excellent for searching static data, but expensive when the data changes often.

There are two useful ways to make an ordered array more update-friendly. One is to split it
into smaller sorted arrays and organize those arrays in levels; this is the basic idea behind
a B+ tree. Another is to collect updates in a small sorted structure and merge them into
larger sorted data later; this is the basic idea behind an LSM tree.

## B+ trees: keep the index ordered while allowing updates

A B+ tree breaks the sorted data into small, sorted nodes arranged as a balanced multiway
tree. Internal nodes hold separator keys and child pointers; leaf nodes hold the actual keys
and values. Since each internal node can point to many children, the tree stays shallow.
Each node is like a small sorted array. The upper levels guide the search to the right leaf,
and splits keep those arrays within their size limit as keys are inserted.

```text
                 [ 30 | 60 ]       internal node
                /     |     \
       [10, 20]   [30, 40, 50]   [60, 70, 80]   leaf nodes
```

To find key `40`, start at the root, follow the child range containing `40`, then search that
leaf. To find a range, seek to its first key and continue through the leaves in sorted order.
Updates change only a small number of nodes; when a node fills, it splits and the tree may
gain a new root.

### Where HDDs and SSDs fit

An **HDD** or **SSD** is persistent storage: it keeps files when the power is off. Both are
below RAM in the computer's storage hierarchy. The operating system and filesystem sit
between an application and the drive:

```text
Database or application
        │ asks to read file bytes
        ▼
Operating system + filesystem
        │ checks the page cache in RAM
        ├── cache hit ───────────────► return cached data
        │
        └── cache miss ─► storage driver/controller ─► HDD or SSD
                                  read data into RAM ◄─────────┘

CPU registers and caches ◄──► RAM (including the OS page cache)
```

The application can ask for a particular byte offset in a file. The filesystem maps that
file offset to locations on the drive. A **logical sector** or **device block** is the unit
the drive exposes for addressing; 512-byte sectors were common on older HDDs. The underlying
physical sector can be larger, such as 4 KiB, and need not match the logical size. The
operating system hides these details from ordinary file reads.

The **page cache** is in RAM, not on the drive. With normal buffered file I/O, the operating
system can keep recently read file data in this cache and buffer writes there before sending
them to storage. If the requested data is already cached, a read needs no drive access. If it
isn't, the OS requests the necessary blocks and keeps the returned data in RAM for later use.
The OS page size is commonly 4 KiB, but can vary by system.

An **SSD** stores data in flash memory chips. It has no moving parts, so it can handle random
reads much faster than an **HDD**, which stores data magnetically on spinning platters and
must move a read head to the requested location. Sequential HDD reads are faster because
nearby data can be read without repeatedly seeking to a new location. Both are still much
slower than RAM, and both transfer data in blocks. An **I/O operation** is a request to read
or write such data; **IOPS** means how many I/O operations a device can complete per second.
Random reads are especially costly for an HDD, but reducing unnecessary reads helps SSDs too.

### Four different meanings of “page” or “block”

These units are related, but they are not the same thing:

| Unit | Layer | What it describes |
|---|---|---|
| Sector/device block | Drive | The block addressed by the storage device, such as a 512-byte or 4 KiB sector. |
| OS memory page | Operating system and RAM | The chunk used for virtual memory and commonly for file caching; often 4 KiB. |
| Database page | Database | The chunk the database chooses to read, write, and organize on disk. |
| Cache line | CPU cache | The small chunk fetched from RAM into CPU cache; commonly 64 bytes. |

A database page can be larger than an OS page and may cover multiple OS pages or device
blocks. The sizes do not have to match. The CPU cache line is smaller still: when the CPU
uses one byte from RAM, the hardware typically fetches the cache line containing it. Cache
line size is hardware-dependent.

Databases commonly choose a page size that is one or more convenient I/O units, then pack
tree nodes to make good use of those pages. The operating system can split, combine, or cache
requests, so this is a design alignment rather than a guarantee that one database page always
becomes exactly one physical device operation.

### Why database pages should hold useful tree nodes

Imagine a database asks for one key in a node that is only 256 bytes. If the OS or device has
to fetch a 4 KiB page/block to get that node, most of that transfer is not needed for this
lookup. The other bytes are not permanently wasted—they may contain a neighboring node that
a later lookup can reuse—but the current lookup used only a small part of what was fetched.

If a B+ tree node is sized to use a database page well, one page read can bring in many
separator keys and child pointers at once. That is the connection between page size and
**fanout**: a node that fits more child pointers can direct a search into more ranges. The
variable `n` in `log_n(N)` is this approximate number of branches per level. More branches
mean fewer levels, and fewer uncached levels usually mean fewer storage reads.

### Why a wide tree needs fewer reads

Imagine a balanced binary search tree with one key and two child choices per node. With one
million keys, it takes about 20 levels to reach a key, because `2^20` is just over one million.
If each level is on a different uncached storage page, a lookup could need about 20 page reads.

A B+ tree node stores many separator keys and child pointers in one page. Suppose each
internal node can direct the search to 100 child pages, and leaves each hold about 100 keys.
Then a tree for about one million keys can be roughly three pages deep:

```text
root page -> internal page -> leaf page containing the key
```

The number of levels follows the branching factor. A binary tree has about `log₂(N)` levels;
a tree with `F` child choices per internal node has about `log_F(N)` levels. For `N = 1,000,000`
and `F = 100`, `log₁₀₀(N) = 3`. Fewer levels usually mean fewer page reads on a lookup.

This is an illustration, not a promise that every lookup causes exactly that many device
reads. The operating system and database can keep frequently used pages in memory, so cached
levels require no disk access. The tree is still designed to keep its height low for the
lookups that do need storage. Larger nodes can hold more branches, but they also take longer
to search and update, so node size is a trade-off.

Wide nodes also reduce pointer overhead. In a binary tree, each key is usually in its own
node with child pointers. In a B+ tree, many keys in a leaf share the same pointer from their
parent. Keys can be packed tightly, and similar keys can sometimes be compressed. The result
is less metadata per key and more useful data in each page.

A B+ tree can also live entirely in RAM. The same compact layout can help, but the unit moving
between RAM and the CPU cache is a much smaller cache line (commonly 64 bytes), rather than a
kilobyte-sized database page. The on-disk benefit from avoiding slow storage reads is
therefore more dramatic; in memory, cache locality and the cost of searching each node matter
more.

## LSM trees: batch updates and merge sorted data

An LSM tree takes a different approach. It accepts recent updates in a small, mutable index,
then periodically merges those updates into larger, sorted files. Merging costs work, but it
avoids rewriting the whole data set for every individual update. The update cost is spread
across later merges; this is called **amortizing** the cost.

```text
writes -> small recent index -> merge -> larger sorted levels
```

Each level contains sorted data. When a small level fills, it is merged into a larger one.
If every flush rewrites one enormous data file, the amount written can greatly exceed the
amount of new data; this is **write amplification**. Multiple levels limit that repeated
rewriting. For example, levels can grow geometrically (roughly 1x, 2x, 4x, 8x ...), so new
data is merged into a similarly sized level before that level is eventually merged upward.
The trade-off is that more levels can mean more places to check during reads. Fewer levels
can mean larger merges and more write amplification.

Newer levels take priority because a key may have older versions in lower levels. A deletion
is recorded as a **tombstone**, which tells lookups to hide an older value. During
**compaction**, levels are merged, obsolete versions and tombstones can be removed, and their
space reclaimed.

LSM trees trade write cost for read work: a point lookup may need to check levels, usually
newest first. A **Bloom filter** can say that a level definitely does not contain a key,
avoiding a read of that level; a positive result only means the key might be there, so the
database still checks. A range query performs a **k-way merge** of sorted results from the
levels, choosing the newest version when the same key appears more than once.

Large levels are often split into multiple sorted files called **SSTables**. This lets
compaction work on pieces over time instead of needing enough free space and time to rewrite
an entire level in one operation. Files within a level can be organized so their key ranges
do not overlap, which helps the database decide which files to inspect.

The small in-memory index for recent writes is called a **MemTable**. It keeps recent values
fast to read while the on-disk levels are being merged. A **write-ahead log (WAL)** records
those updates so the MemTable can be rebuilt after a crash. The WAL and MemTable both contain
recent updates for different purposes: the WAL provides recovery, while the MemTable provides
an index for reads. The MemTable can use a B+ tree, skip list, or another ordered structure.

## Choosing between them

| Structure | Point lookup | Range lookup | Update pattern |
|---|---|---|---|
| Hash table | Fast on average | Not naturally ordered | May require expensive resizing |
| Sorted array | `O(log N)` search | Efficient ordered traversal | Insertions and deletions can cost `O(N)` |
| B+ tree | `O(log N)` tree search | Efficient ordered traversal | Updates local nodes; splits maintain balance |
| LSM tree | May check multiple levels | Merge ordered results | Buffers writes and merges data in batches |

B+ trees keep one updatable ordered structure, with page-oriented nodes that limit lookup
depth. LSM trees make updates cheaper by batching them, then pay for merges and potentially
more work during reads. Both solve problems that a plain sorted array cannot handle well as
the data grows and changes.
