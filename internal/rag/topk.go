package rag

import "sort"

type scoredIndex struct {
	index int
	score float64
}

// topScoredIndices retains only the requested result window. The worst retained
// candidate is the heap root; callers supply a total order including ties.
type topScoredIndices struct {
	items  []scoredIndex
	limit  int
	better func(scoredIndex, scoredIndex) bool
}

func newTopScoredIndices(limit int, better func(scoredIndex, scoredIndex) bool) topScoredIndices {
	return topScoredIndices{items: make([]scoredIndex, 0, limit), limit: limit, better: better}
}

func (t *topScoredIndices) add(item scoredIndex) {
	if t.limit == 0 {
		return
	}
	if len(t.items) < t.limit {
		t.items = append(t.items, item)
		for child := len(t.items) - 1; child > 0; {
			parent := (child - 1) / 2
			if !t.better(t.items[parent], t.items[child]) {
				break
			}
			t.items[parent], t.items[child] = t.items[child], t.items[parent]
			child = parent
		}
		return
	}
	if !t.better(item, t.items[0]) {
		return
	}
	t.items[0] = item
	for parent := 0; ; {
		child := 2*parent + 1
		if child >= len(t.items) {
			break
		}
		if right := child + 1; right < len(t.items) && t.better(t.items[child], t.items[right]) {
			child = right
		}
		if !t.better(t.items[parent], t.items[child]) {
			break
		}
		t.items[parent], t.items[child] = t.items[child], t.items[parent]
		parent = child
	}
}

func (t *topScoredIndices) sorted() []scoredIndex {
	sort.Slice(t.items, func(i, j int) bool { return t.better(t.items[i], t.items[j]) })
	return t.items
}
