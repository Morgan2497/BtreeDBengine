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

## B+ trees: keep the index ordered while allowing updates

A B+ tree breaks the sorted data into small, sorted nodes arranged as a balanced multiway
tree. Internal nodes hold separator keys and child pointers; leaf nodes hold the actual keys
and values. Since each internal node can point to many children, the tree stays shallow.

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

## LSM trees: batch updates and merge sorted data

An LSM tree takes a different approach. It accepts recent updates in a small, mutable index,
then periodically merges those updates into larger, sorted files. Merging costs work, but it
avoids rewriting the whole data set for every individual update.

```text
writes -> small recent index -> merge -> larger sorted levels
```

Each level contains sorted data. Newer levels take priority because a key may have older
versions in lower levels. A deletion is recorded as a **tombstone**, which tells lookups to
hide an older value. During compaction, levels are merged, obsolete versions and tombstones
can be removed, and their space reclaimed.

LSM trees trade write cost for read work: a lookup may need to check several levels. A Bloom
filter can quickly say that a level definitely does not contain a key, avoiding some reads.
Range queries combine the ordered results from the levels. Implementations often split levels
into smaller sorted files so that compaction can proceed gradually.

The small in-memory index for recent writes is often called a **MemTable**. Sorted files on
disk are commonly called **SSTables**. The MemTable can be a B+ tree, skip list, or another
ordered structure; a write-ahead log can help recover its recent updates after a crash.

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
