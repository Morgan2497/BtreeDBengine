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

### HDDs, SSDs, and I/O operations

An **HDD** stores data on spinning magnetic disks. To read a location, it moves a mechanical
head to the right track and waits for the disk to rotate to the data. That makes a random read
costly: the next requested key may be far away from the previous one. Reading nearby data in
sequence is much more efficient.

An **SSD** stores data in flash memory and has no moving head or spinning disk. It can handle
random reads much faster than an HDD, but a read still takes time and the device still moves
data in blocks. So reducing the number of reads helps on both kinds of storage, especially
when many queries are competing to use the device.

An **I/O operation** is a request to read or write a block of data. **IOPS** means how many
such requests a device can complete per second. A database lookup that needs several blocks
from storage uses several I/O operations. If the device has a limited IOPS rate, those reads
limit how many lookups it can serve at once. The time for each read also affects how long one
lookup takes.

Storage and software use different block sizes. Older hard drives commonly transferred data
in 512-byte sectors. The operating system also caches file data in memory pages, commonly
4 KiB, though the exact sizes depend on the hardware and OS. A database can choose its own
logical **page** size for reading and writing its file. A B+ tree node is stored in one or
more of these database pages. Keeping nodes aligned to the I/O unit avoids reading a block
while using only a small fraction of it. The database page does not have to be the same size
as an OS memory page or a physical disk sector.

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
