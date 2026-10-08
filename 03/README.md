# B+ Trees and Crash Recovery

A database index must solve two problems together: find and change records efficiently, and
preserve a recoverable state if an update stops halfway through. A B+ tree organizes the
records. Fixed-size pages provide storage for its nodes. Copy-on-write lets an update build
a new tree version while keeping the previous version readable.

## 1. Read the diagrams first: nodes, keys, values, and pointers

A **node** is a container holding multiple entries. A **key** is one item used to order and
find data. A key does not itself have children. An internal node contains keys and child
pointers; a leaf contains keys and their values.

In the diagrams below, an internal entry has the form `minimum-key:child-name`. The key is
the smallest key reachable through that child. For example:

```text
                     R [10:A | 40:B | 70:C]
                       /        |        \
                      v         v         v
               A [10,20,30] B [40,50,60] C [70,80,90]
```

`R` is one internal node with three child pointers. `A`, `B`, and `C` are leaf nodes. The
leaves hold the actual records; their values are omitted to keep the drawings readable.
The `40` in the root is a routing key, while the `40` in leaf `B` belongs to the stored
record. Duplicating the key in the root does not duplicate the record's value.

To look up `50`, choose the root entry whose starting key is the largest one no greater
than `50`: that is `40:B`. Follow its pointer, then search leaf `B`.

```text
R: 50 >= 40 and 50 < 70 -> choose B
B: [40,50,60]           -> find 50 and return its value
```

A search or insertion below the first child's stored minimum goes to that first child. For
example, looking for `5` reaches `A` and reports that it is absent; inserting `5` puts it in
`A` and changes the routing minimum to `5`. An implementation can also use a special smallest
sentinel key to represent the first range.

This convention stores a starting key for every child. Another common representation
stores only the boundaries between children: three child pointers need two separator keys.
Both represent the same ranges. Keep the convention consistent when reading a diagram or
implementing the tree.

A **balanced** B+ tree has all its leaves at the same depth. A **shallow** tree has few levels
from root to leaf. Internal nodes can have many children, so a large number of records can
fit under a relatively short tree.

The number of children can vary between nodes. For example, a 2-3-4 tree allows internal
nodes with two, three, or four children; a split changes how those children are grouped while
preserving the leaf depths. A B+ tree uses the same idea with capacities chosen for its nodes.

Keeping record values out of internal nodes leaves more room for routing entries. As a rough
byte-budget illustration, suppose a 4,096-byte node uses 128 bytes for its header. That leaves
3,968 bytes. If a routing entry takes 8 bytes for its key and 8 for its pointer, about
`floor(3968 / 16) = 248` entries fit. Adding a 128-byte value to every routing entry would
reduce that estimate to `floor(3968 / 144) = 27`. Actual encodings have other overheads, but
the reason is the same: omitting values can increase fanout and reduce tree height.

## 2. The mathematics: why nested sorted arrays help

### One large sorted array

Binary search repeatedly halves the remaining possibilities. For `N` keys, the number of
halvings grows as `log₂(N)`, so lookup is `O(log N)`.

Insertion can be much slower. Inserting into the middle of a contiguous sorted array shifts
later entries to make a gap. Deletion shifts entries to close a gap. In the worst case that
is `O(N)` work. Replacing a fixed-size value for an existing key need not perform those
shifts; the expensive updates here are insertions and deletions.

### Two levels: a directory and smaller arrays

Let `m` be the number of smaller arrays, and distribute `N` keys approximately evenly among
them. Each smaller array contains about `N/m` keys. A sorted directory contains `m`
references and routing keys.

```text
Directory: m entries
     |              |                         |
     v              v                         v
 array 1         array 2                   array m
 N/m keys        N/m keys                  N/m keys
```

Finding a key performs two searches:

1. Binary-search the directory: `O(log m)`.
2. Binary-search the selected array: `O(log(N/m))`.

The logarithm identity explains the total:

```text
log₂(m) + log₂(N/m)
= log₂(m × N/m)
= log₂(N)
```

For a million keys divided into a thousand groups, search roughly a thousand directory
entries and then a thousand leaf entries. Each search takes about ten comparisons, giving
about twenty altogether. This is an estimate, not an exact count for every search.

Updating the selected array can shift `O(N/m)` entries. Maintaining a flat directory when a
group is added or removed can shift `O(m)` references. The first cost describes an ordinary
leaf insertion; the second becomes relevant when the structure changes.

To balance the possible shifting costs, choose:

```text
m = N/m
m² = N
m = √N

O(m + N/m) = O(√N + √N) = O(√N)
```

Why not make many more groups? That reduces the size of each leaf array, but enlarges the
directory that may need shifting. Why not make fewer groups? That makes the directory
smaller but leaves larger arrays to shift. Balancing the two lengths minimizes this simple
two-level design's worst shifting cost. A million-key example balances both at about a
thousand entries.

### More levels: bound every node's size

Instead of leaving one growing directory above the leaves, divide the directory into
smaller nodes too. Continue until every node fits its size limit. This creates a B+ tree.

Let `F` be the typical internal fanout, `L` the typical number of records per leaf, and `H`
the number of node levels, counting both root and leaf. For a reasonably occupied tree:

```text
record capacity ≈ L × F^(H-1)
H ≈ 1 + log_F(N/L)
```

Heights are whole numbers, so a capacity calculation rounds the required number of levels
up. If all records already fit in one leaf (`N <= L`), that leaf is the root and `H = 1`.
The formula above describes larger trees with typical occupancy rather than guaranteeing
an exact height for every layout.

For example, suppose leaves hold about 100 records and internal nodes have about 100
children. Three node levels can cover approximately:

```text
root -> 100 internal nodes -> 10,000 leaves -> 1,000,000 records
                                             100 per leaf
```

A lookup visits one node at each level. If binary search within a node takes `O(log s)`
comparisons and the fanout is proportional to its entry capacity `s`, then:

```text
levels × work per level
≈ log_s(N) × log₂(s)
= log₂(N)
```

Thus lookup remains `O(log N)` in comparisons. When a visited node occupies one database
page, the number of uncached page reads is roughly the path length `H`, rather than the
number of comparisons inside those pages.

Insertion shifts entries inside a bounded leaf, costing at most `O(s)`. A split can cause
more bounded work at each ancestor. With `s` fixed, even a cascade through the full path
costs `O(log N)` in this simplified model. Fixed page size does not mean a split is free;
it means a node's local work does not grow with the entire database.

These height estimates assume occupancy is maintained sensibly. Merely allowing nonempty
nodes is not enough to prevent long chains of one-child internal nodes. Merge thresholds,
redistribution policies, and root collapse help keep the structure compact.

## 3. Maintaining a B+ tree: the rules updates must preserve

An update must preserve sorted routing ranges, keep every leaf at the same depth, and keep
nodes within their size limit. A nonempty tree must not contain reachable empty nodes.
The empty database is a special case: it can have no root.

For the following examples, use these deliberately small limits:

- A leaf holds at most **three records**.
- An internal node holds at most **three child pointers**.
- An internal node stores each child's minimum key, using the convention defined above.

Real disk nodes are limited in **bytes**, so variable-length keys can overflow a page before
reaching any fixed key count. These small limits make the structural changes visible.

### 3.1 Insert without overflowing a node

Start with one leaf that is also the root:

```text
root -> A [10,30]
```

Insert `20` in sorted order:

```text
root -> A [10,20,30]
```

There is still room for three records, so no split is needed. The node count and tree height
remain unchanged. If the tree has parents, a change to a child's minimum key must also be
reflected in its parent's routing entry.

### 3.2 A leaf split that propagates to the root

**Step 1 — Start with a full root and three full leaves.**

```text
                      R [10:A | 40:B | 70:C]
                        /        |        \
                       v         v         v
                A [10,20,30] B [40,50,60] C [70,80,90]
```

**Step 2 — Seek the leaf for `35`, then insert it.** The route is `10:A`, since `35` is
between `10` and `40`. In memory, temporarily construct the updated sequence:

```text
                      R [10:A | 40:B | 70:C]
                        /        |        \
                       v         v         v
             A [10,20,30,35] B [40,50,60] C [70,80,90]
               OVERFLOW: 4 > 3
```

This oversized sequence is a working result. It must be repaired before publishing a valid
tree or storing it as a single fixed-size node.

**Step 3 — Split the leaf into two sorted leaves.**

```text
Old child A: [10,20,30,35]

Replacement children:
    A1 [10,20]     A2 [30,35]
```

Every record stays in a leaf. The second leaf's minimum `30` is copied into the parent as
a routing key. It is not removed from leaf `A2`.

**Step 4 — Replace one parent entry with two.** `10:A` becomes `10:A1` and `30:A2`.

```text
                 R [10:A1 | 30:A2 | 40:B | 70:C]
                    /       |        |       \
                   v        v        v        v
             A1 [10,20] A2 [30,35] B [40,50,60] C [70,80,90]

R now has 4 child pointers: OVERFLOW, because its limit is 3.
```

The overflow has **propagated upward**. The leaf split succeeded, but its new child pointer
made the parent too large.

**Step 5 — Split the overflowing internal node.** Divide its child entries into two nodes:

```text
       P [10:A1 | 30:A2]                 Q [40:B | 70:C]
           /        \                      /       \
          v          v                    v         v
     A1 [10,20]  A2 [30,35]          B [40,50,60] C [70,80,90]
```

This split moves routing entries and pointers; the leaf records below those pointers stay
where they are. Both `P` and `Q` are within the three-child limit.

**Step 6 — Because the old root split, create a new root.**

```text
                         Z [10:P | 40:Q]
                           /         \
                          v           v
                P [10:A1 | 30:A2]   Q [40:B | 70:C]
                    /      \           /       \
                   v        v         v         v
              A1 [10,20] A2 [30,35] B [40,50,60] C [70,80,90]
```

Before insertion, a path visited `R -> leaf`: two node levels. After repair, a path visits
`Z -> P or Q -> leaf`: three levels. Every leaf gained the same extra level, so the tree is
still balanced. The height grows only when a split reaches the root and creates a new root.

### 3.3 A split can stop at an ordinary parent

Use the final tree above. Insert `25` into `A1`; it fits:

```text
                         Z [10:P | 40:Q]
                           /         \
                P [10:A1 | 30:A2]   Q [40:B | 70:C]
                    /      \           /       \
          A1 [10,20,25] A2 [30,35] B [40,50,60] C [70,80,90]
```

Now insert `27`. `A1` overflows as `[10,20,25,27]`, so split it into `A11 [10,20]` and
`A12 [25,27]`. Replace `P`'s one reference to `A1` with two references:

```text
                            Z [10:P | 40:Q]
                              /         \
               P [10:A11 | 25:A12 | 30:A2]  Q [40:B | 70:C]
                     /       |       \         /       \
                    v        v        v       v         v
              A11 [10,20] A12 [25,27] A2 [30,35] B [...] C [...]
```

`P` now has three children and fits. Its minimum remains `10`, so `Z`'s entry for `P` is
unchanged. The split stops here; the tree stays three levels tall. If `P` had overflowed,
its parent would receive replacement entries in exactly the same way as in the previous
example. That repair can continue until a parent fits or the root splits.

### 3.4 Deletion, empty nodes, upward propagation, and root shrinkage

Reset to the tree from Step 6, before the later `25` and `27` insertions:

```text
                         Z [10:P | 40:Q]
                           /         \
                P [10:A1 | 30:A2]   Q [40:B | 70:C]
                    /      \           /       \
              A1 [10,20] A2 [30,35] B [40,50,60] C [70,80,90]
```

For this first deletion example, use a simple rule: remove empty nodes, while leaving
nonempty sparse nodes in place. A more eager merge policy is explained afterward.

**Step 1 — Delete `35`.** Seek `Z -> P -> A2`, then remove the record:

```text
                         Z [10:P | 40:Q]
                           /         \
                P [10:A1 | 30:A2]   Q [40:B | 70:C]
                    /      \           /       \
              A1 [10,20] A2 [30]   B [40,50,60] C [70,80,90]
```

`A2` is nonempty, and its minimum is still `30`. Under this example's policy, no merge or
parent change is needed.

**Step 2 — Delete `30`.** `A2` becomes empty:

```text
                         Z [10:P | 40:Q]
                           /         \
                P [10:A1 | 30:A2]   Q [40:B | 70:C]
                    /      \           /       \
              A1 [10,20] A2 []     B [40,50,60] C [70,80,90]
                         EMPTY
```

**Step 3 — Remove the empty leaf and its parent entry.** Conceptually, merging an empty
leaf with its sibling is `[] + [10,20] = [10,20]`. No records need to move; keep the sibling
and eliminate the empty node's branch.

```text
                         Z [10:P | 40:Q]
                           /         \
                      P [10:A1]     Q [40:B | 70:C]
                           |           /       \
                           v          v         v
                      A1 [10,20] B [40,50,60] C [70,80,90]
```

`P` still has one child. It is sparse but nonempty. All leaves still have the same depth.
Do not bypass `P` on only this branch: that would put `A1` at a different depth from `B` and
`C`. Root collapse is a different operation that affects all remaining paths together.

**Step 4 — Delete `20`.** This changes only the remaining leaf:

```text
                         Z [10:P | 40:Q]
                           /         \
                      P [10:A1]     Q [40:B | 70:C]
                           |           /       \
                           v          v         v
                        A1 [10]  B [40,50,60] C [70,80,90]
```

**Step 5 — Delete `10`, remove empty `A1`, and repair `P`.** `P` now has no children:

```text
                         Z [10:P | 40:Q]
                           /         \
                         P []      Q [40:B | 70:C]
                         EMPTY         /       \
                                      v         v
                                B [40,50,60] C [70,80,90]
```

This is the upward propagation of deletion: removing a leaf's last record removed a child
pointer, which made its parent empty too.

**Step 6 — Remove empty `P` from `Z`.** The root now has exactly one child:

```text
                         Z [40:Q]
                             |
                             v
                       Q [40:B | 70:C]
                           /       \
                          v         v
                    B [40,50,60] C [70,80,90]
```

**Step 7 — Replace the root with its only child.** `Z` provides no routing choice, so make
`Q` the root:

```text
                      root -> Q [40:B | 70:C]
                                  /       \
                                 v         v
                           B [40,50,60] C [70,80,90]
```

Every remaining leaf loses one level at the same time. The tree shrinks from three levels
to two and stays balanced. The root can be collapsed again if its replacement is another
one-child internal node. If all records are deleted, the tree becomes empty.

### 3.5 Earlier merging and redistribution

Waiting for nodes to become empty can leave many sparsely filled pages. An implementation
may try merging when a node falls below a chosen size threshold instead.

For example, two leaf siblings with the same parent contain one record each:

```text
Before:  P [10:A | 30:B]       After: P [10:M]
               /    \                       |
           A [10]  B [30]                M [10,30]
```

Their combined contents fit the three-record leaf limit. The parent loses one child pointer;
that parent may then need its own merge. Only merge compatible nodes at the same level, and
check that the combined node fits the size limit. Internal-node merging combines child
entries rather than leaf records.

If the combined contents are too large, some implementations redistribute entries between
siblings instead of merging:

```text
Before: P [10:A | 20:B]        After: P [10:A | 30:B]
              /    \                        /    \
          A [10] B [20,30,40]          A [10,20] B [30,40]
```

The parent boundary must change to `30`, because `B`'s new minimum is `30`. Redistribution
is a policy choice; it is not required for every B+ tree implementation.

### 3.6 The repair logic in plain steps

Insertion follows one root-to-leaf path. Insert in the leaf. If it fits, return its updated
reference to the parent. If it overflows, split it and return references to the replacement
nodes. The parent replaces the old child entry with those entries and repeats the size check.
If the root splits, create a new root.

Deletion follows one path too. Remove the record, update any changed minimum key, and remove
or merge nodes according to the occupancy policy. Each removed or merged child changes the
parent's entries, so repair may propagate upward. Collapse an internal root with one child,
or mark the tree empty if it has no records.

A changed minimum can propagate even without a split or merge. Here, deleting `10` from
`A1 [10,20]` changes the first child's minimum at two levels:

```text
Before: Z [10:P | 40:Q]          After: Z [20:P | 40:Q]
                |                               |
        P [10:A1 | 30:A2]               P [20:A1 | 30:A2]
                |                               |
           A1 [10,20]                         A1 [20]

Q and A2 are unchanged; the tree shape and height are unchanged.
```

Ignoring those routing changes could misroute later lookups or updates. Minimum-key
maintenance and capacity maintenance are separate responsibilities.

The recursive insertion idea can be expressed as pseudocode. These are private working
nodes; publishing a new disk root comes later:

```text
insert(node, key, value):
    working = copy node's own entries
    if working is a leaf:
        insert or replace the record in sorted order
    else:
        child_index = choose the key's child range
        replacements = insert(chosen child, key, value)
        replace that child's entry with entries for replacements
        set each replacement entry's routing key to its child's minimum

    if working fits:
        return [working]
    else:
        return split working into nodes that each fit

root_replacements = insert(root, key, value)
if there is one replacement:
    new root = that node
else:
    new root = an internal node pointing to the replacements
```

An empty tree starts by creating a leaf. The pseudocode's returned list explains propagation:
one child entry might be replaced by two, so the caller must check its own size. It does not
mean the entire tree is searched or rebuilt.

Splitting and merging describe the logical tree shape. Copy-on-write, explained below,
determines which physical pages store the new shape without overwriting the old version.

## 4. Putting the tree on disk: pages and allocation

### 4.1 What `malloc`, `free`, and garbage collection mean

In a running program, `malloc` asks a memory allocator for RAM and `free` returns that RAM
for reuse. Go usually provides allocations through ordinary objects and slices, with garbage
collection reclaiming unreachable memory. These manage the process's memory.

An on-disk tree needs a separate allocator for regions of its database file. The OS does not
know that bytes at a certain offset represent an obsolete B+ tree node. Go's garbage
collector cannot decide that a database page is safe to reuse just because a temporary Go
buffer is no longer needed. The database must track its own disk-page ownership and lifetime.
It still uses ordinary RAM allocation for buffers; the extra responsibility is allocation
inside the persistent file.

### 4.2 Fixed-size block allocation

Suppose a database page is 4,096 bytes. Divide the file into equal slots:

```text
file offset:     0        4,096       8,192      12,288      16,384
              +----------+----------+----------+----------+----------+
page ID:      | P0       | P1       | P2       | P3       | P4       |
contents:     | metadata | root     | leaf A   | leaf B   | free     |
              +----------+----------+----------+----------+----------+
```

An internal node can store a page ID as its child pointer. That ID is a stable file location,
not a process memory address:

```text
byte offset = page ID × page size
P3 begins at 3 × 4,096 = 12,288 bytes
```

The database reads the bytes at that offset and decodes the node. A cached page can be served
from RAM; a cache miss can require storage I/O. Fixed-size allocation makes every page slot
interchangeable and avoids needing a general variable-size disk allocator.

### 4.3 Allocating a page, step by step

**Step 1 — Request a page for a new node.** Suppose `P4` is already free:

```text
live pages: P0, P1, P2, P3
free list: [P4]
```

**Step 2 — Remove a safe free page from the free list.**

```text
new node receives P4
free list becomes []
```

**Step 3 — Encode the new node and write it into that slot.**

```text
P4: old unused bytes -> encoded new node
```

If no reusable slot exists, extend the file or reserve another unused slot at its end. With
the file above, that could allocate `P5` at offset `20,480`.

### 4.4 Freeing a page and the free list

A **free list** records page IDs whose slots may be reused. It can be represented by pages
of IDs, a bitmap, or another allocator structure. Freeing a slot usually does not remove its
bytes from the file or shorten the file. It changes the allocator's metadata so a later
allocation may overwrite that slot.

```text
P2 becomes unnecessary and is safe to reuse
        |
        v
free list: [] -> [P2]
        |
next allocation pops P2
        v
P2 stores a different node
```

“Safe to reuse” matters. A page must not be reachable from the current committed tree, from
an active reader's older snapshot, or from a retained recovery version. Sharing a node
between versions does not make it free. Free-list updates also need a crash-safe persistence
protocol; losing allocation metadata or prematurely freeing a live page can corrupt the tree.

## 5. Copy-on-write: copy one path and preserve the old version

An in-place write changes an existing node's bytes. If it is interrupted, the node may be
partly old and partly new. **Copy-on-write (COW)** allocates replacement nodes instead, leaving
the old reachable nodes intact until a new root can be published safely.

### 5.1 What a shallow copy means here

Suppose an internal node contains keys and pointers to two child pages:

```text
Original P: [a:A | c:C]
                 |       |
                 v       v
                 A       C
```

A **shallow copy** creates a separate copy of the node's own entries but keeps the child
references pointing to the same children:

```text
P  [a:A | c:C] -> A (page P30), C (page P31)
P' [a:A | c:C] -> A (page P30), C (page P31)

There is one A page and one C page, each referenced by both parents.
There are two separate parent containers: P and P'.
```

The repeated child labels above denote shared pages. The pointer table shows their identities:

| Parent | First child | Second child |
|---|---|---|
| P | page A | page C |
| P' | page A | page C |

A deep copy would recursively copy those children and their descendants. COW avoids that
work by copying only the nodes on the changed path. Sharing is safe because published pages
are treated as immutable: a future update also creates replacements.

For RAM buffers, ensure the copied node really has its own writable bytes. Assigning one Go
slice variable to another shares the backing array; it does not copy the node's bytes.
Copying the node's byte buffer creates independent entries, while its encoded child page
IDs still identify shared children.

### 5.2 Updating a leaf: follow the page IDs

Start with a three-level tree. The page numbers below are illustrative:

```text
current root -> R (P10)
                /    \
               v      v
          P (P20)    E (P21, unchanged subtree)
            /  \
           v    v
       A (P30) C (P31, contains c=3)
```

**Step 1 — Change `c=3` to `c=11` in a copied leaf.** Allocate `P40`; old `P31` stays intact.

```text
old route: R(P10) -> P(P20) -> C(P31: c=3)
new leaf:                     C'(P40: c=11)
```

At this point, following the current root still returns `c=3`. The new leaf exists but has
not been connected to a new root.

**Step 2 — Shallow-copy the parent, then replace only its C pointer.** Allocate `P41`:

```text
old P(P20): [A -> P30 | C  -> P31]
new P'(P41):[A -> P30 | C' -> P40]
                  ^
                  shared A page
```

We must not change `P20`'s pointer in place, because the old root still depends on that node.

**Step 3 — Shallow-copy the root and replace its P pointer.** Allocate `P42`:

```text
old R(P10): [P  -> P20 | E -> P21]
new R'(P42):[P' -> P41 | E -> P21]
                              ^
                              shared E subtree
```

Now two complete versions exist:

```text
OLD VERSION                       NEW VERSION
R(P10)                            R'(P42)
 /   \                             /    \
P(P20) E(P21)                     P'(P41) E(P21)
 /  \                              /  \
A(P30) C(P31:c=3)                 A(P30) C'(P40:c=11)

A(P30) and E(P21) are shared pages, not duplicated pages.
```

**Step 4 — Publish the new root through the commit protocol.** New readers can begin from
`P42`. Existing readers that captured `P10` can still use the old version.

```text
before publication: current root = P10 -> c=3
after publication:  current root = P42 -> c=11
```

The entire tree is not copied. This update copied the leaf, its parent, and its root. A
three-level path produced three replacement nodes. Splits and merges can produce additional
replacement nodes, but unchanged subtrees remain shared.

**Step 5 — Retire obsolete pages, then reclaim when safe.** `P10`, `P20`, and `P31` are
old-version pages. They may still be needed by readers or recovery. `P30` and `P21` remain
live in the new version and must not be reclaimed.

```text
retired but still needed by a snapshot: P10, P20, P31
shared and still live:                  P30, P21
newly live:                            P42, P41, P40

after all relevant old-version users finish:
eligible old pages -> free list -> later reuse
```

### 5.3 Root publication is the crash-sensitive step

A filename rename changes a filesystem name mapping. A COW tree commit changes a database
root reference in metadata. They share the idea of publishing a prepared replacement, but
they operate on different structures. An ordinary on-disk pointer write is not automatically
atomic or durable.

The required ordering is conceptually:

```text
1. Write all new tree pages.
2. Sync those pages so the new root cannot refer to missing durable children.
3. Publish the new root with a recoverable metadata protocol.
4. Make that publication durable before reporting a durable commit.
```

The metadata protocol must account for interrupted writes, for example through versioned
metadata records and validation with a recoverable older record. Its precise implementation
is a separate part of the engine; COW by itself does not solve a torn root record.

| Crash point | What recovery needs |
|---|---|
| While writing new pages, before publication | Keep using the old valid root. Incomplete new pages are not reachable through it. |
| After new pages are durable but before publication | The old root still represents the committed state; new pages may need later cleanup. |
| During root-metadata publication | Validate the metadata and select a complete recoverable version. Do not follow arbitrary torn pointer bytes. |
| After a correctly completed durable publication | Use the new root and its already durable pages. |

A failed sync means persistence is uncertain, even if a read through the OS page cache sees
new data. Commit error handling and metadata recovery must handle that uncertainty.

## 6. Snapshots, snapshot isolation, and concurrency

### 6.1 Why an old root is a snapshot

A **snapshot** is a stable view of a particular tree version. A reader captures a root once
and uses it throughout its operation or transaction. Because published nodes are immutable,
new versions cannot change the contents reachable through that root.

```text
Time 1: reader Alice captures root R1
        R1 -> c=3

Time 2: writer builds replacement pages and publishes root R2
        R1 -> c=3       R2 -> c=11

Time 3: Alice reads again using R1 -> still c=3
        reader Bob starts using R2 -> c=11
```

Alice must continue to use `R1`; reading the global current root before every query would
mix versions. Retaining `R1` is meaningful only while its reachable pages remain allocated.

### 6.2 What this provides for snapshot isolation

Copy-on-write supplies the versioned storage needed for stable transaction reads. A
transaction can begin by retaining one root and see a consistent version even as other
transactions commit newer versions.

Full **snapshot isolation** also requires transaction rules: related changes must be
published as one commit, and conflicting writers need coordination or conflict detection.
Stable snapshots alone do not provide those rules. Snapshot isolation also does not by
itself guarantee serializable execution of every concurrent transaction.

For an initial implementation, serializing writers is simpler than allowing several writers
to publish competing roots. Later designs can add validation and retry rules for multiple
writers.

### 6.3 Multiple readers and one writer

While Alice reads `R1`, the writer modifies private copies rather than Alice's pages:

```text
reader Alice -> R1 -> immutable old pages
                         (writer does not modify these)

writer       -> private new pages -> complete R2 -> publish

reader Bob   -> R2 -> immutable new/shared pages
```

This allows readers to traverse their snapshots without waiting for the writer to finish
constructing a new version. Root capture/publication and reader registration still need
brief coordination, and page reclamation must respect active readers.

With two uncoordinated writers, both could start from `R1`, produce different successors,
and publish them one after another. The second publication could overwrite the first
writer's root choice and lose its update. A writer lock, or a suitable validation/retry
protocol, prevents this problem.

### 6.4 Why the free list must wait for readers

Suppose Alice is still using the old `C` page `P31`. Reusing `P31` immediately after publishing
`R2` would let Alice follow an old pointer into unrelated new data:

```text
Unsafe: R1 -> P31, but P31 was overwritten for another node
        Alice's snapshot is now broken.

Safe:   R1 -> old P31 until Alice finishes
        retire P31 -> wait -> add P31 to the free list
```

An engine can track active versions or reader lifetimes to decide when retired pages become
reusable. COW permits concurrent snapshots; careful reclamation keeps those snapshots valid.

## 7. An alternative recovery method: saved full-page copies

COW copies a path to the root, increasing the bytes written per logical change. This is
**write amplification**. Another approach saves complete replacement page images before
overwriting their original locations.

```text
Step 1: prepare full after-image
        main page: [a=1,b=2]   recovery copy: [a=2,b=4]

Step 2: make validated recovery information durable
        main page: [a=1,b=2]   recovery copy: [a=2,b=4] durable

Step 3: overwrite main page; imagine a crash halfway through
        main page: [???????]   recovery copy: [a=2,b=4] durable

Step 4: recover by writing the full saved image again
        main page: [a=2,b=4]   recovery copy: [a=2,b=4]
```

Reapplying the same full replacement page is **idempotent**: applying it twice produces the
same page as applying it once. In this simplified protocol, a checksum rejects an incomplete
recovery copy before main-page overwriting is allowed. Durable valid copies can repair the
main pages if a later overwrite is interrupted. Real engines also need metadata describing
which copies belong to a complete committed recovery operation; a checksum alone is not a
transaction commit marker.

This family of techniques includes double-write protection and full-page recovery images.
The exact commit/acknowledgment rules depend on the engine. COW preserves an old complete
state while building a new one; saved after-images preserve enough information to reconstruct
the new pages. Saving before-images instead can support restoring old pages.

**Logical logging** describes operations such as `SET a=2`. **Physical logging** describes
page changes or replacement page contents. A torn page may not be safe to interpret as a
normal tree node, so full physical page images can repair it without first trusting its
structure. Logical logs can also participate in recovery in engines with additional
protocols; they simply do not, on their own, repair arbitrary torn page bytes.

## 8. Terms to keep distinct

| Term | Meaning in this design |
|---|---|
| Key | An ordered lookup identifier. |
| Node | A container of leaf records or internal routing entries. |
| Fanout | The number of child pointers in an internal node. |
| Height | The number of node levels on a root-to-leaf path, using this document's counting convention. |
| Split | Replace an oversized node with smaller nodes and update its parent. |
| Merge | Combine compatible sibling nodes that fit together and remove a parent branch. |
| Root collapse | Make a sole child the root, reducing every remaining path's depth together. |
| Database page | A fixed-size region of a database file used for storage/allocation. |
| Disk pointer | A page ID or file offset identifying a node's persistent location. |
| Free list | Allocator metadata identifying reusable disk-page slots. |
| Shallow copy | Copy a node's own contents while initially retaining its child references. |
| Copy-on-write | Build replacement nodes and publish a new root while retaining old reachable nodes. |
| Snapshot | A stable tree version reached through a retained root. |
| Persistent data structure | A structure that retains accessible old versions; this term does not itself promise power-loss durability. |
| Durability | A correctly committed state survives failure under the storage protocol's guarantees. |
| Write amplification | More storage bytes are written than the size of the logical data change. |
| Idempotent | Repeating an operation has the same final effect as applying it once. |

Efficient lookup depends on balanced, bounded nodes with useful occupancy. Safe disk updates
depend on retaining or reconstructing a complete state, publishing roots recoverably, and
reusing pages only after no live version needs them.
