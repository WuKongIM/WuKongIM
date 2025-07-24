package wkutil

// ArrayContains ArrayContains
func ArrayContains(items []string, target string) bool {

	for _, element := range items {
		if target == element {
			return true

		}
	}
	return false

}

func ArrayEqual(items1 []string, items2 []string) bool {
	if len(items1) != len(items2) {
		return false
	}
	for i, element := range items1 {
		if element != items2[i] {
			return false
		}
	}
	return true
}

func ArrayContainsUint64(items []uint64, target uint64) bool {
	if len(items) == 0 {
		return false
	}
	for _, element := range items {
		if target == element {
			return true

		}
	}
	return false

}

func ArrayContainsUint32(items []uint32, target uint32) bool {
	for _, element := range items {
		if target == element {
			return true

		}
	}
	return false

}

func RemoveUint64(items []uint64, target uint64) []uint64 {
	for i, element := range items {
		if target == element {
			return append(items[:i], items[i+1:]...)
		}
	}
	return items
}

func ArrayEqualUint64(items1 []uint64, items2 []uint64) bool {
	if len(items1) != len(items2) {
		return false
	}
	for i, element := range items1 {
		if element != items2[i] {
			return false
		}
	}
	return true
}
