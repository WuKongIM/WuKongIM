package wkutil

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"time"
)

// BoolToInt bool值转换为int值
func BoolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// IntToBool int值转换为bool
func IntToBool(b int) bool {
	if b == 1 {
		return true
	}
	return false
}

// BoolToInt bool值转换为int值
func BoolToUint8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// IntToBool int值转换为bool
func Uint8ToBool(b uint8) bool {

	return b == 1
}

// ToJSON 将对象转换为JSON
func ToJSON(obj interface{}) string {
	jsonData, err := json.Marshal(obj)
	if err != nil {
		return ""
	}

	return string(jsonData)
}

// JSONToMap JsonToMap
func JSONToMap(json string) (map[string]interface{}, error) {
	var resultMap map[string]interface{}
	err := ReadJSONByByte([]byte(json), &resultMap)
	return resultMap, err
}

// ReadJSONByByte 读取JSON
func ReadJSONByByte(body []byte, obj interface{}) error {
	mdz := json.NewDecoder(bytes.NewBuffer(body))

	mdz.UseNumber()
	err := mdz.Decode(obj)

	if err != nil {
		return err
	}
	return nil
}

var tenToAny map[int]string = map[int]string{
	0:  "0",
	1:  "1",
	2:  "2",
	3:  "3",
	4:  "4",
	5:  "5",
	6:  "6",
	7:  "7",
	8:  "8",
	9:  "9",
	10: "a",
	11: "b",
	12: "c",
	13: "d",
	14: "e",
	15: "f",
	16: "g",
	17: "h",
	18: "i",
	19: "j",
	20: "k",
	21: "l",
	22: "m",
	23: "n",
	24: "o",
	25: "p",
	26: "q",
	27: "r",
	28: "s",
	29: "t",
	30: "u",
	31: "v",
	32: "w",
	33: "x",
	34: "y",
	35: "z",
	36: "A",
	37: "B",
	38: "C",
	39: "D",
	40: "E",
	41: "F",
	42: "G",
	43: "H",
	44: "I",
	45: "J",
	46: "K",
	47: "L",
	48: "M",
	49: "N",
	50: "O",
	51: "P",
	52: "Q",
	53: "R",
	54: "S",
	55: "T",
	56: "U",
	57: "V",
	58: "W",
	59: "X",
	60: "Y",
	61: "Z"}

// DecimalToAny 10进制转任意进制
func DecimalToAny(num int64, n int) string {
	newNumStr := ""
	var remainder int64
	var remainderString string
	for num != 0 {
		remainder = num % int64(n)
		if 76 > remainder && remainder > 9 {
			remainderString = tenToAny[int(remainder)]
		} else {
			remainderString = strconv.Itoa(int(remainder))
		}
		newNumStr = remainderString + newNumStr
		num = num / int64(n)
	}
	return newNumStr
}

// map根据value找key
func findKey(in string) int {
	result := -1
	for k, v := range tenToAny {
		if in == v {
			result = k
		}
	}
	return result
}

// AnyToDecimal 任意进制转10进制
func AnyToDecimal(num string, n int) int64 {
	var newNum float64
	newNum = 0.0
	nNum := len(strings.Split(num, "")) - 1
	for _, value := range strings.Split(num, "") {
		tmp := float64(findKey(value))
		if tmp != -1 {
			newNum = newNum + tmp*math.Pow(float64(n), float64(nNum))
			nNum = nNum - 1
		} else {
			break
		}
	}
	return int64(newNum)
}

// GetRandomString 生成随机字符串
func GetRandomString(num int) string {
	str := "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	bytes := []byte(str)
	result := []byte{}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < num; i++ {
		result = append(result, bytes[r.Intn(len(bytes))])
	}
	return string(result)
}

// RemoveRepeatedElement 高性能去重函数，使用 map 实现 O(n) 时间复杂度
func RemoveRepeatedElement(arr []string) []string {
	if len(arr) == 0 {
		return arr
	}

	// 使用 map 来跟踪已见过的元素，保持原始顺序
	seen := make(map[string]bool, len(arr))
	result := make([]string, 0, len(arr))

	for _, item := range arr {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}

	return result
}

// RemoveRepeatedElementOfUint64 高性能去重函数，使用 map 实现 O(n) 时间复杂度
func RemoveRepeatedElementOfUint64(arr []uint64) []uint64 {
	if len(arr) == 0 {
		return arr
	}

	// 使用 map 来跟踪已见过的元素，保持原始顺序
	seen := make(map[uint64]bool, len(arr))
	result := make([]uint64, 0, len(arr))

	for _, item := range arr {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}

	return result
}

func Uint32ArrayToStringArray(arr []uint32) (newArr []string) {
	newArr = make([]string, 0)
	for _, v := range arr {
		newArr = append(newArr, strconv.Itoa(int(v)))
	}
	return
}

func Base64Decode(str string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(str)
}
