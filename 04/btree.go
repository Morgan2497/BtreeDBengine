package byodb04

import (
	"bytes"
	"encoding/binary"
	"errors"
)

/*
* In a B+tree, only leaf nodes store value, and only internal nodes have children.
 */

// node format:
//  ----HEADER----
// | type | nkeys |  pointers  |   offsets  | key-values
// |  2B  |   2B  | nkeys * 8B | nkeys * 2B | ...

// key-value format:
// | klen | vlen | key | val | ------------------------
// |  2B  |  2B  | ... | ... |                        |
//														     -------------------+-------------------
// Ex: a leaf node contains {"k1": "hi", "k3": "hello"} is encoded.
// | type | nkeys | pointers | offsets | key-values 										| unused |
// |  2   |   2   | nil nil  |  8  19  | 2 2 "k1" "hi" 2 5 "k3" "hello" |
// |  2B  |  2B   |  2 × 8B  |  2 × 2B | 4B + 2B + 2B + 4B + 2B + 5B    |        |
// type = 2 means this is a leaf node.
// nkyes = 2 means it contains two keys.
// 2, 2 = value and key length for hi
// 2, 5 = value and key lengt for hello
// 8, 19 for offests = stores numbers tjat tell you where the encoded pairs end, measured from 
// 									   the beginning of the key-values area. 
// they are measured from the start of the key-values area, not from the start of the whole node.

const HEADER = 4
const BTREE_PAGE_SIZE = 4096
const BTREE_MAX_KEY_SIZE = 1000
const BTREE_MAX_VAL_SIZE = 3000

func init() {
	node1max := HEADER + 1*8 + 1*2 + 4 + BTREE_MAX_KEY_SIZE + BTREE_MAX_VAL_SIZE
	assert(node1max <= BTREE_PAGE_SIZE) // maximum KV
}

const (
	BNODE_NODE = 1 // intenral nodes without values.
	BNODE_LEAF = 2 // leaf nodes with values.
)

type BNode []byte // can be dumped to the disk.

type BTree struct {
	// pointer (a nonzero page number)
	root uint64
	// callbacks for managing on-disk pages
	get func(uint64) []byte // dereference a pointer
	new func([]byte) uint64 // allocate a new page
	del func(uint64)        // dealocate a page
}

// Header fields.
// getters.
func (node BNode) btype() uint16 { // node or leaf?
	return binary.LittleEndian.Uint16(node[0:2])
}

func (node BNode) nkeys() uint16 { // # of keys?
	return binary.LittleEndian.Uint16(node[2:4])
}

func (node BNode) setHeader(btype uint16, nkeys uint16) {
	binary.LittleEndian.PutUint16(node[0:2], btype)
	binary.LittleEndian.PutUint16(node[2:4], nkeys)
}

// Child page numbers. Leaf nodes leave these slots at zero.
func (node BNode) getPtr(idx uint16) uint64 {
	assert(idx < node.nkeys())
	pos := HEADER + 8*idx
	return binary.LittleEndian.Uint64(node[pos:])
}

func (node BNode) setPtr(idx uint16, val uint64) {
	assert(idx < node.nkeys())
	pos := HEADER + 8*idx
	binary.LittleEndian.PutUint64(node[pos:], val)
}

// An offset records the end of a KV entry relative to the KV area.
func (node BNode) getOffset(idx uint16) uint16 {
	if idx == 0 {
		return 0
	}
	assert(idx <= node.nkeys())
	pos := HEADER + 8*node.nkeys() + 2*(idx-1)
	return binary.LittleEndian.Uint16(node[pos:])
}

func (node BNode) setOffset(idx uint16, offset uint16) {
	assert(1 <= idx && idx <= node.nkeys())
	pos := HEADER + 8*node.nkeys() + 2*(idx-1)
	binary.LittleEndian.PutUint16(node[pos:], offset)
}

func (node BNode) kvPos(idx uint16) uint16 {
	assert(idx <= node.nkeys())
	return HEADER + 8*node.nkeys() + 2*node.nkeys() + node.getOffset(idx)
}

func (node BNode) getKey(idx uint16) []byte {
	assert(idx < node.nkeys())
	pos := node.kvPos(idx)
	klen := binary.LittleEndian.Uint16(node[pos:])
	return node[pos+4:][:klen]
}

func (node BNode) getVal(idx uint16) []byte {
	assert(idx < node.nkeys())
	pos := node.kvPos(idx)
	//                                 node[pos : pos+2]
	klen := binary.LittleEndian.Uint16(node[pos:])
	//                                 node[pos+2 : pos+4]
	vlen := binary.LittleEndian.Uint16(node[pos+2:])
	return node[pos+4+klen:][:vlen]
}

func (node BNode) nbytes() uint16 {
	return node.kvPos(node.nkeys())
}

func assert(cond bool) {
	if !cond {
		panic("assertion failure")
	}
}

// Append one entry to a new node after its header has been set.
func nodeAppendKV(new BNode, idx uint16, ptr uint64, key []byte, val []byte) {
	new.setPtr(idx, ptr)
	pos := new.kvPos(idx)
	binary.LittleEndian.PutUint16(new[pos:], uint16(len(key)))
	binary.LittleEndian.PutUint16(new[pos+2:], uint16(len(val)))
	copy(new[pos+4:], key)
	copy(new[pos+4+uint16(len(key)):], val)
	new.setOffset(idx+1, new.getOffset(idx)+4+uint16(len(key)+len(val)))
}

func leafInsert(new BNode, old BNode, idx uint16, key []byte, val []byte) {
	new.setHeader(BNODE_LEAF, old.nkeys()+1)
	nodeAppendRange(new, old, 0, 0, idx) // copy the keys before 'idx'
	nodeAppendKV(new, idx, 0, key, val)  // the new key
	nodeAppendRange(new, old, idx+1, idx, old.nkeys()-idx)
}

func leafDelete(new BNode, old BNode, idx uint16) {
	new.setHeader(BNODE_LEAF, old.nkeys()-1)

	// copy entries before the deleted entry.
	nodeAppendRange(new, old, 0, 0, idx)

	// skip the deleted entry and copy the remaining entries.
	nodeAppendRange(new, old, idx, idx+1, old.nkeys()-(idx+1))
}

// merge 2 nodes into 1.
func nodeMerge(new BNode, left BNode, right BNode) {
	new.setHeader(left.btype(), left.nkeys()+right.nkeys())
	nodeAppendRange(new, left, 0, 0, left.nkeys())
	nodeAppendRange(new, right, left.nkeys(), 0, right.nkeys())
	assert(new.nbytes() <= BTREE_PAGE_SIZE)
}


// copy multiple keys, values, and pointers into the position.
func nodeAppendRange(new BNode, old BNode, dstNew uint16, srcOld uint16, n uint16) {
	for i := uint16(0); i < n; i++ {
		dst, src := dstNew+i, srcOld+i
		nodeAppendKV(new, dst, old.getPtr(src), old.getKey(src), old.getVal(src))
	}
}

// if the key already exists, we should update the value insetead.
func leafUpdate(new BNode, old BNode, idx uint16, key []byte, val []byte) {
	new.setHeader(BNODE_LEAF, old.nkeys())
	nodeAppendRange(new, old, 0, 0, idx)
	nodeAppendKV(new, idx, 0, key, val)
	nodeAppendRange(new, old, idx+1, idx+1, old.nkeys()-(idx+1))
}

// Replace one internal child link without changing its routing key.
func nodeReplaceKid1ptr(new BNode, old BNode, idx uint16, ptr uint64) {
	copy(new, old[:old.nbytes()])
	new.setPtr(idx, ptr)
}

// found the last position that is less than or equal to the key.
func nodeLookupLE(node BNode, key []byte) uint16 {
	nkeys := node.nkeys()
	var i uint16

	for i = 0; i < nkeys; i++ {
		cmp := bytes.Compare(node.getKey(i), key)
		if cmp == 0 {
			return i
		}
		if cmp > 0 {
			return i - 1
		}
	}
	return i - 1
}


// split a node by balancing bytes.
// an oversized node can be split into 2 nodes, each with half of the keys (in-memory B+tree)
// Split an oversized node into 2 nodes. The 2nd node always fits.
func nodeSplit2(left BNode, right BNode, old BNode) {
	assert(old.nkeys() >= 2)

	// the initial guess.
	nleft := old.nkeys() / 2

	// try to fit the left half
	left_bytes := func() uint16 {
		return 4 + 8*nleft + 2*nleft + old.getOffset(nleft)
	}

	for left_bytes() > BTREE_PAGE_SIZE {
		nleft--
	}
	assert(nleft >= 1)

	// try to fit the right half.
	right_bytes := func() uint16 {
		return old.nbytes() - left_bytes() + 4
	}
	for right_bytes() > BTREE_PAGE_SIZE {
		nleft++
	}
	assert(nleft < old.nkeys())
	nright := old.nkeys() - nleft

	// new nodes.
	left.setHeader(old.btype(), nleft)
	right.setHeader(old.btype(), nright)
	nodeAppendRange(left, old, 0, 0, nleft)
	nodeAppendRange(right, old, 0, nleft, nright)
	// NOTE: the left half maybe still too big.
	assert(right.nbytes() <= BTREE_PAGE_SIZE)
}

// the left half may still be too large because while fitting the right half,
// the left half size grows. This can happen if there is a big key in the middle, requiring
// another split.
func nodeSplit3(old BNode) (uint16, [3]BNode) {
	if old.nbytes() <= BTREE_PAGE_SIZE {
		old = old[:BTREE_PAGE_SIZE]
		return 1, [3]BNode{old} // not split
	}

	left := BNode(make([]byte, 2*BTREE_PAGE_SIZE)) // might be split later.
	right := BNode(make([]byte, BTREE_PAGE_SIZE))

	nodeSplit2(left, right, old)

	if left.nbytes() <= BTREE_PAGE_SIZE {
		left = left[:BTREE_PAGE_SIZE]
		return 2, [3]BNode{left, right} // 2 nodes.
	}

	leftleft := BNode(make([]byte, BTREE_PAGE_SIZE))
	middle := BNode(make([]byte, BTREE_PAGE_SIZE))
	nodeSplit2(leftleft, middle, left)
	assert(leftleft.nbytes() <= BTREE_PAGE_SIZE)
	return 3, [3]BNode{leftleft, middle, right} // 3 nodes.
}

// Tree insertion is a top-down recursion until reaching the leaf node.
func treeInsert(tree *BTree, node BNode, key []byte, val []byte) BNode {
	// the extra size allows it to exceed 1 page temporarily.
	new := BNode(make([]byte, 2*BTREE_PAGE_SIZE))
	// where to insert the key?
	idx := nodeLookupLE(node, key) // node.getKey(idx) <= key
	switch node.btype() {
	case BNODE_LEAF:
		if bytes.Equal(key, node.getKey(idx)) {
			leafUpdate(new, node, idx, key, val) // found, update it.
		} else {
			leafInsert(new, node, idx+1, key, val) // not found, insert.
		}

	case BNODE_NODE: // internal node, walk into the child node.
		// recursive insertion to the kid node.
		kptr := node.getPtr(idx)
		knode := treeInsert(tree, tree.get(kptr), key, val)
		// after insertion, split the result.
		nsplit, split := nodeSplit3(knode)
		// deallocate the old kid node.
		tree.del(kptr)
		// update the kid links.
		nodeReplaceKidN(tree, new, node, idx, split[:nsplit]...)
	default:
		panic("bad node!")
	}
	return new
}

// Replace an internal child link with one or more updated children.
func nodeReplaceKidN(tree *BTree, new BNode, old BNode, idx uint16, kids ...BNode) {
	inc := uint16(len(kids))
	if inc == 1 && bytes.Equal(kids[0].getKey(0), old.getKey(idx)) {
		nodeReplaceKid1ptr(new, old, idx, tree.new(kids[0]))
		return
	}

	new.setHeader(BNODE_NODE, old.nkeys()+inc-1)
	nodeAppendRange(new, old, 0, 0, idx)
	for i, kid := range kids {
		nodeAppendKV(new, idx+uint16(i), tree.new(kid), kid.getKey(0), nil)
	}
	nodeAppendRange(new, old, idx+inc, idx+1, old.nkeys()-(idx+1))
}

func nodeReplace2Kid (new BNode, old BNode, idx uint16, ptr uint64, key []byte) {
	new.setHeader(BNODE_NODE, old.nkeys()-1)

	// copy parent entries before the two being replaced.
	nodeAppendRange(new, old, 0, 0, idx)	

	// Write the link to the merged child.
	nodeAppendKV(new, idx, ptr, key, nil)
	
	// skip both old links and copy the remaining entries.
	nodeAppendRange(new, old, idx+1, idx+2, old.nkeys()-(idx+2))
}

/* High-level interfaces to use the B+tree as a KV. */

// Insert a new key or update an existing key.
func (tree *BTree) Insert(key []byte, val []byte) error {
	// 1. check the length limit imposed by the node format.
	if err := checkLimit(key, val); err != nil {
		return err
	}

	// 2. create the first node.
	if tree.root == 0 {
		root := BNode(make([]byte, BTREE_PAGE_SIZE))
		root.setHeader(BNODE_LEAF, 2)
		// a dummy key, this makes the tree cover the whole key space.
		// thus a lookup can always find a containing node.
		nodeAppendKV(root, 0, 0, nil, nil)
		nodeAppendKV(root, 1, 0, key, val)
		tree.root = tree.new(root)
		return nil
	}

	// 3. insert the key.
	node := treeInsert(tree, tree.get(tree.root), key, val)

	// 4. grow the tree if the root is split.
	nsplit, split := nodeSplit3(node)
	tree.del(tree.root)
	if nsplit > 1 {
		root := BNode(make([]byte, BTREE_PAGE_SIZE))
		root.setHeader(BNODE_NODE, nsplit)
		for i, knode := range split[:nsplit] {
			ptr, key := tree.new(knode), knode.getKey(0)
			nodeAppendKV(root, uint16(i), ptr, key, nil)
		}
		tree.root = tree.new(root)
	} else {
		tree.root = tree.new(split[0])
	}
	return nil
}

func (tree *BTree) Delete(key []byte) (bool, error) {
	if err := checkLimit(key, nil); err != nil {
		return false, err // the only way for an update to fail
	}

	if tree.root == 0 {
		return false, nil
	}

	updated := treeDelete(tree, tree.get(tree.root), key)
	if len(updated) == 0 {
		return false, nil // not found
	}

	tree.del(tree.root)
	if updated.btype() == BNODE_NODE && updated.nkeys() == 1 {
		// remove a level 
		tree.root = updated.getPtr(0)
	} else {
		tree.root = tree.new(updated)
	}
	return true, nil
}

func nodeGetKey(tree *BTree, node BNode, key []byte) ([]byte, bool) {
	idx := nodeLookupLE(node, key)
	switch node.btype() {
	case BNODE_LEAF:
		if bytes.Equal(key, node.getKey(idx)) {
			return node.getVal(idx), true
		} else {
			return nil, false
		}
	case BNODE_NODE:
		return nodeGetKey(tree, tree.get(node.getPtr(idx)), key)
	default:
		panic("bad node!")
	}
}

func (tree *BTree) Get(key []byte) ([]byte, bool) {
	if tree.root == 0 {
		return nil, false
	}
	return nodeGetKey(tree, tree.get(tree.root), key)
}

// merge nodes: since we're copy-on-write, updating a node means copying data to a new node.
// Deleted keys means unused space within nodes. In the worst case, a mostly empty tree
// can still retain a large number of nodes. We can improve this by triggering merges earlier
// using 1/4 of a page as a threshold.

// deleting may result in empty nodes, which can be merged with its sibling if it has one.
// should the updated kid be merged with a sibling?
func shouldMerge(tree *BTree, node BNode, idx uint16, updated BNode) (int, BNode) {
	if updated.nbytes() > BTREE_PAGE_SIZE/4 {
		return 0, BNode{}
	}

	// left merge.
	if idx > 0 {
		sibling := BNode(tree.get(node.getPtr(idx - 1)))
		merged := sibling.nbytes() + updated.nbytes() - HEADER
		if merged <= BTREE_PAGE_SIZE {
			return -1, sibling // -1  indicates the left direction.
		}
	}

	// right merge.
	if idx+1 < node.nkeys() {
		sibling := BNode(tree.get(node.getPtr(idx + 1)))
		merged := sibling.nbytes() + updated.nbytes() - HEADER
		if merged <= BTREE_PAGE_SIZE {
			return +1, sibling // +1 indicates the right direction.
		}
	}

	return 0, BNode{}
}

func checkLimit(key []byte, val []byte) error {
	if len(key) ==0 {
		return errors.New("empty key") // for a dummy key.
	}

	if len(key) > BTREE_MAX_KEY_SIZE {
		return errors.New("key too long")
	}

	if len(val) > BTREE_MAX_VAL_SIZE {
		return errors.New("value too long")
	}

	return nil
}

// delete a key from the tree.
func treeDelete(tree *BTree, node BNode, key []byte) BNode {
	// where do find the key?
	idx := nodeLookupLE(node, key)

	// act depending on the node type.
	switch node.btype() {
	case BNODE_LEAF:
		if !bytes.Equal(key, node.getKey(idx)) {
			return BNode{} // not found
		}
		// delete the key in the leaf.
		new := BNode(make([]byte, BTREE_PAGE_SIZE))
		leafDelete(new, node, idx)
		return new

	case BNODE_NODE:
		return nodeDelete(tree, node, idx, key)

	default:
		panic("bad node!")
	}
}

// delete a key from an internal node; part of the treeDelete()
func nodeDelete(tree *BTree, node BNode, idx uint16, key []byte) BNode {
	// recurse into the kid.
	kptr := node.getPtr(idx)
	updated := treeDelete(tree, tree.get(kptr), key)
	if len(updated) == 0 {
		return BNode{} // not found.
	}
	tree.del(kptr)

	// check for merging.
	new := BNode(make([]byte, BTREE_PAGE_SIZE))
	mergeDir, sibling := shouldMerge(tree, node, idx, updated)

	switch {
	case mergeDir < 0: // left
		merged := BNode(make([]byte, BTREE_PAGE_SIZE))
		nodeMerge(merged, sibling, updated)
		tree.del(node.getPtr(idx - 1))
		nodeReplace2Kid(new, node, idx-1, tree.new(merged), merged.getKey(0))
	
	case mergeDir > 0: // right
		merged := BNode(make([]byte, BTREE_PAGE_SIZE))
		nodeMerge(merged, updated, sibling)
		tree.del(node.getPtr(idx+1))
		nodeReplace2Kid(new, node, idx, tree.new(merged), merged.getKey(0))
	
	case mergeDir == 0 && updated.nkeys() == 0:
		assert(node.nkeys() == 1 && idx == 0) // 1 empty child but no sibling.
		new.setHeader(BNODE_NODE, 0) // the parent becomes empty too.
	
	case mergeDir == 0 && updated.nkeys() > 0: // no merge.
		nodeReplaceKidN(tree, new, node, idx, updated)
	}
	return new
}
