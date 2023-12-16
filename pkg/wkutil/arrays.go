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

func ArrayContainsUint64(items []uint64, target uint64) bool {
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
