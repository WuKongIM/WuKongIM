package wkutil

import (
	"sort"
)

// RemoveRepeatedElementGeneric 通用的去重函数，使用泛型实现
func RemoveRepeatedElementGeneric[T comparable](arr []T) []T {
	if len(arr) == 0 {
		return arr
	}
	
	// 使用 map 来跟踪已见过的元素，保持原始顺序
	seen := make(map[T]bool, len(arr))
	result := make([]T, 0, len(arr))
	
	for _, item := range arr {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	
	return result
}

// RemoveRepeatedElementSorted 对于已排序数组的高性能去重
// 时间复杂度 O(n)，空间复杂度 O(1)（原地操作）
func RemoveRepeatedElementSorted[T comparable](arr []T) []T {
	if len(arr) <= 1 {
		return arr
	}
	
	writeIndex := 1
	for readIndex := 1; readIndex < len(arr); readIndex++ {
		if arr[readIndex] != arr[readIndex-1] {
			arr[writeIndex] = arr[readIndex]
			writeIndex++
		}
	}
	
	return arr[:writeIndex]
}

// RemoveRepeatedElementAndSort 去重并排序，适合不需要保持原始顺序的场景
func RemoveRepeatedElementAndSort[T comparable](arr []T) []T {
	if len(arr) == 0 {
		return arr
	}
	
	// 先排序
	sort.Slice(arr, func(i, j int) bool {
		return compareGeneric(arr[i], arr[j])
	})
	
	// 然后去重
	return RemoveRepeatedElementSorted(arr)
}

// compareGeneric 通用比较函数
func compareGeneric[T comparable](a, b T) bool {
	// 这里需要根据具体类型实现比较逻辑
	// 对于基本类型，Go 会自动处理
	switch any(a).(type) {
	case string:
		return any(a).(string) < any(b).(string)
	case int, int8, int16, int32, int64:
		return any(a).(int64) < any(b).(int64)
	case uint, uint8, uint16, uint32, uint64:
		return any(a).(uint64) < any(b).(uint64)
	case float32, float64:
		return any(a).(float64) < any(b).(float64)
	default:
		// 对于其他类型，使用字符串比较
		return false
	}
}

// RemoveRepeatedElementOptimized 针对不同大小数组的优化版本
func RemoveRepeatedElementOptimized[T comparable](arr []T) []T {
	if len(arr) == 0 {
		return arr
	}
	
	// 对于小数组，使用简单的线性搜索可能更快
	if len(arr) <= 10 {
		return removeRepeatedSmallArray(arr)
	}
	
	// 对于大数组，使用 map 方法
	return RemoveRepeatedElementGeneric(arr)
}

// removeRepeatedSmallArray 针对小数组的优化去重
func removeRepeatedSmallArray[T comparable](arr []T) []T {
	result := make([]T, 0, len(arr))
	
	for i, item := range arr {
		found := false
		// 在已处理的元素中查找
		for j := 0; j < i; j++ {
			if arr[j] == item {
				found = true
				break
			}
		}
		if !found {
			result = append(result, item)
		}
	}
	
	return result
}

// RemoveRepeatedElementInPlace 原地去重，修改原数组
// 返回去重后的长度，原数组的前 n 个元素是去重结果
func RemoveRepeatedElementInPlace[T comparable](arr []T) int {
	if len(arr) <= 1 {
		return len(arr)
	}
	
	seen := make(map[T]bool, len(arr))
	writeIndex := 0
	
	for _, item := range arr {
		if !seen[item] {
			seen[item] = true
			arr[writeIndex] = item
			writeIndex++
		}
	}
	
	return writeIndex
}

// RemoveRepeatedElementWithCapacity 预分配容量的去重函数
func RemoveRepeatedElementWithCapacity[T comparable](arr []T, estimatedUniqueCount int) []T {
	if len(arr) == 0 {
		return arr
	}
	
	if estimatedUniqueCount <= 0 {
		estimatedUniqueCount = len(arr)
	}
	
	seen := make(map[T]bool, estimatedUniqueCount)
	result := make([]T, 0, estimatedUniqueCount)
	
	for _, item := range arr {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	
	return result
}

// StringSliceDedup 专门针对字符串切片的高性能去重
func StringSliceDedup(arr []string) []string {
	return RemoveRepeatedElementOptimized(arr)
}

// Uint64SliceDedup 专门针对 uint64 切片的高性能去重
func Uint64SliceDedup(arr []uint64) []uint64 {
	return RemoveRepeatedElementOptimized(arr)
}

// IntSliceDedup 专门针对 int 切片的高性能去重
func IntSliceDedup(arr []int) []int {
	return RemoveRepeatedElementOptimized(arr)
}

// RemoveRepeatedElementStats 带统计信息的去重函数
type DedupStats struct {
	OriginalCount int
	UniqueCount   int
	DuplicateCount int
	DuplicationRate float64
}

func RemoveRepeatedElementWithStats[T comparable](arr []T) ([]T, DedupStats) {
	originalCount := len(arr)
	result := RemoveRepeatedElementGeneric(arr)
	uniqueCount := len(result)
	duplicateCount := originalCount - uniqueCount
	
	var duplicationRate float64
	if originalCount > 0 {
		duplicationRate = float64(duplicateCount) / float64(originalCount) * 100
	}
	
	stats := DedupStats{
		OriginalCount:   originalCount,
		UniqueCount:     uniqueCount,
		DuplicateCount:  duplicateCount,
		DuplicationRate: duplicationRate,
	}
	
	return result, stats
}

// RemoveRepeatedElementBatch 批量去重，适合处理多个数组
func RemoveRepeatedElementBatch[T comparable](arrays ...[]T) [][]T {
	results := make([][]T, len(arrays))
	
	for i, arr := range arrays {
		results[i] = RemoveRepeatedElementGeneric(arr)
	}
	
	return results
}

// RemoveRepeatedElementParallel 并行去重，适合处理大数组
// 注意：这个函数会打乱原始顺序
func RemoveRepeatedElementParallel[T comparable](arr []T, numWorkers int) []T {
	if len(arr) == 0 || numWorkers <= 1 {
		return RemoveRepeatedElementGeneric(arr)
	}
	
	// 将数组分割成多个部分
	chunkSize := len(arr) / numWorkers
	if chunkSize == 0 {
		chunkSize = 1
	}
	
	type result struct {
		index int
		data  []T
	}
	
	resultChan := make(chan result, numWorkers)
	
	// 启动工作协程
	for i := 0; i < numWorkers; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if i == numWorkers-1 {
			end = len(arr) // 最后一个工作者处理剩余的所有元素
		}
		
		go func(index int, chunk []T) {
			dedupChunk := RemoveRepeatedElementGeneric(chunk)
			resultChan <- result{index: index, data: dedupChunk}
		}(i, arr[start:end])
	}
	
	// 收集结果
	chunks := make([][]T, numWorkers)
	for i := 0; i < numWorkers; i++ {
		res := <-resultChan
		chunks[res.index] = res.data
	}
	
	// 合并所有块
	var merged []T
	for _, chunk := range chunks {
		merged = append(merged, chunk...)
	}
	
	// 对合并后的结果再次去重
	return RemoveRepeatedElementGeneric(merged)
}
