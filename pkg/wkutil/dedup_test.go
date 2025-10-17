package wkutil

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

// 生成测试数据
func generateStringSlice(size int, duplicateRate float64) []string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	uniqueCount := int(float64(size) * (1 - duplicateRate))
	if uniqueCount < 1 {
		uniqueCount = 1
	}

	// 生成唯一值
	unique := make([]string, uniqueCount)
	for i := 0; i < uniqueCount; i++ {
		unique[i] = fmt.Sprintf("item_%d", i)
	}

	// 生成包含重复的数组
	result := make([]string, size)
	for i := 0; i < size; i++ {
		result[i] = unique[r.Intn(uniqueCount)]
	}

	return result
}

func generateUint64Slice(size int, duplicateRate float64) []uint64 {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	uniqueCount := int(float64(size) * (1 - duplicateRate))
	if uniqueCount < 1 {
		uniqueCount = 1
	}

	// 生成唯一值
	unique := make([]uint64, uniqueCount)
	for i := 0; i < uniqueCount; i++ {
		unique[i] = uint64(i)
	}

	// 生成包含重复的数组
	result := make([]uint64, size)
	for i := 0; i < size; i++ {
		result[i] = unique[r.Intn(uniqueCount)]
	}

	return result
}

// 旧的实现（用于性能对比）
func removeRepeatedElementOld(arr []string) []string {
	newArr := make([]string, 0, len(arr))
	for i := 0; i < len(arr); i++ {
		repeat := false
		for j := i + 1; j < len(arr); j++ {
			if arr[i] == arr[j] {
				repeat = true
				break
			}
		}
		if !repeat {
			newArr = append(newArr, arr[i])
		}
	}
	return newArr
}

func removeRepeatedElementOfUint64Old(arr []uint64) []uint64 {
	newArr := make([]uint64, 0, len(arr))
	for i := 0; i < len(arr); i++ {
		repeat := false
		for j := i + 1; j < len(arr); j++ {
			if arr[i] == arr[j] {
				repeat = true
				break
			}
		}
		if !repeat {
			newArr = append(newArr, arr[i])
		}
	}
	return newArr
}

// 基准测试：字符串去重
func BenchmarkStringDedup(b *testing.B) {
	sizes := []int{10, 100, 1000, 10000}
	duplicateRates := []float64{0.1, 0.5, 0.9} // 10%, 50%, 90% 重复率

	for _, size := range sizes {
		for _, rate := range duplicateRates {
			data := generateStringSlice(size, rate)

			b.Run(fmt.Sprintf("Old_Size%d_Dup%.0f%%", size, rate*100), func(b *testing.B) {
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					// 复制数据避免修改原数组
					testData := make([]string, len(data))
					copy(testData, data)
					_ = removeRepeatedElementOld(testData)
				}
			})

			b.Run(fmt.Sprintf("New_Size%d_Dup%.0f%%", size, rate*100), func(b *testing.B) {
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					// 复制数据避免修改原数组
					testData := make([]string, len(data))
					copy(testData, data)
					_ = RemoveRepeatedElement(testData)
				}
			})

			b.Run(fmt.Sprintf("Generic_Size%d_Dup%.0f%%", size, rate*100), func(b *testing.B) {
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					// 复制数据避免修改原数组
					testData := make([]string, len(data))
					copy(testData, data)
					_ = RemoveRepeatedElementGeneric(testData)
				}
			})

			b.Run(fmt.Sprintf("Optimized_Size%d_Dup%.0f%%", size, rate*100), func(b *testing.B) {
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					// 复制数据避免修改原数组
					testData := make([]string, len(data))
					copy(testData, data)
					_ = RemoveRepeatedElementOptimized(testData)
				}
			})
		}
	}
}

// 基准测试：uint64 去重
func BenchmarkUint64Dedup(b *testing.B) {
	sizes := []int{10, 100, 1000, 10000}
	duplicateRates := []float64{0.1, 0.5, 0.9}

	for _, size := range sizes {
		for _, rate := range duplicateRates {
			data := generateUint64Slice(size, rate)

			b.Run(fmt.Sprintf("Old_Size%d_Dup%.0f%%", size, rate*100), func(b *testing.B) {
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					testData := make([]uint64, len(data))
					copy(testData, data)
					_ = removeRepeatedElementOfUint64Old(testData)
				}
			})

			b.Run(fmt.Sprintf("New_Size%d_Dup%.0f%%", size, rate*100), func(b *testing.B) {
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					testData := make([]uint64, len(data))
					copy(testData, data)
					_ = RemoveRepeatedElementOfUint64(testData)
				}
			})
		}
	}
}

// 基准测试：原地去重
func BenchmarkInPlaceDedup(b *testing.B) {
	data := generateStringSlice(1000, 0.5)

	b.Run("InPlace", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			testData := make([]string, len(data))
			copy(testData, data)
			_ = RemoveRepeatedElementInPlace(testData)
		}
	})

	b.Run("Regular", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			testData := make([]string, len(data))
			copy(testData, data)
			_ = RemoveRepeatedElement(testData)
		}
	})
}

// 功能测试
func TestRemoveRepeatedElement(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "empty slice",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "no duplicates",
			input:    []string{"a", "b", "c"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "with duplicates",
			input:    []string{"a", "b", "a", "c", "b"},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "all same",
			input:    []string{"a", "a", "a"},
			expected: []string{"a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 测试新实现
			result := RemoveRepeatedElement(tt.input)
			if !equalStringSlices(result, tt.expected) {
				t.Errorf("RemoveRepeatedElement() = %v, want %v", result, tt.expected)
			}

			// 测试泛型实现
			resultGeneric := RemoveRepeatedElementGeneric(tt.input)
			if !equalStringSlices(resultGeneric, tt.expected) {
				t.Errorf("RemoveRepeatedElementGeneric() = %v, want %v", resultGeneric, tt.expected)
			}
		})
	}
}

func TestRemoveRepeatedElementOfUint64(t *testing.T) {
	tests := []struct {
		name     string
		input    []uint64
		expected []uint64
	}{
		{
			name:     "empty slice",
			input:    []uint64{},
			expected: []uint64{},
		},
		{
			name:     "no duplicates",
			input:    []uint64{1, 2, 3},
			expected: []uint64{1, 2, 3},
		},
		{
			name:     "with duplicates",
			input:    []uint64{1, 2, 1, 3, 2},
			expected: []uint64{1, 2, 3},
		},
		{
			name:     "all same",
			input:    []uint64{1, 1, 1},
			expected: []uint64{1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RemoveRepeatedElementOfUint64(tt.input)
			if !equalUint64Slices(result, tt.expected) {
				t.Errorf("RemoveRepeatedElementOfUint64() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestRemoveRepeatedElementWithStats(t *testing.T) {
	input := []string{"a", "b", "a", "c", "b", "a"}
	result, stats := RemoveRepeatedElementWithStats(input)

	expectedResult := []string{"a", "b", "c"}
	if !equalStringSlices(result, expectedResult) {
		t.Errorf("Result = %v, want %v", result, expectedResult)
	}

	if stats.OriginalCount != 6 {
		t.Errorf("OriginalCount = %d, want 6", stats.OriginalCount)
	}

	if stats.UniqueCount != 3 {
		t.Errorf("UniqueCount = %d, want 3", stats.UniqueCount)
	}

	if stats.DuplicateCount != 3 {
		t.Errorf("DuplicateCount = %d, want 3", stats.DuplicateCount)
	}

	expectedRate := 50.0
	if stats.DuplicationRate != expectedRate {
		t.Errorf("DuplicationRate = %f, want %f", stats.DuplicationRate, expectedRate)
	}
}

// 辅助函数
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalUint64Slices(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 示例测试
func ExampleRemoveRepeatedElement() {
	input := []string{"apple", "banana", "apple", "cherry", "banana"}
	result := RemoveRepeatedElement(input)
	fmt.Println(result)
	// Output: [apple banana cherry]
}

func ExampleRemoveRepeatedElementWithStats() {
	input := []string{"a", "b", "a", "c", "b", "a"}
	result, stats := RemoveRepeatedElementWithStats(input)
	fmt.Printf("Result: %v\n", result)
	fmt.Printf("Original: %d, Unique: %d, Duplicates: %d, Rate: %.1f%%\n",
		stats.OriginalCount, stats.UniqueCount, stats.DuplicateCount, stats.DuplicationRate)
	// Output:
	// Result: [a b c]
	// Original: 6, Unique: 3, Duplicates: 3, Rate: 50.0%
}
