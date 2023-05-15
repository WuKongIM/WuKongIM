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
