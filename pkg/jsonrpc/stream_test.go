package jsonrpc

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// 定义一个用于测试的简单结构体
type SimpleMsg struct {
	ID      int    `json:"id"`
	Message string `json:"message"`
}

func TestDecodeConcatenatedJSONStream(t *testing.T) {
	testCases := []struct {
		name          string
		input         string
		expectedObjs  []SimpleMsg // 预期的成功解码对象
		expectedCount int         // 预期成功解码的数量
		expectedError error       // 预期循环结束后遇到的错误 (io.EOF 或其他解码错误)
	}{
		{
			name:  "Two valid objects",
			input: `{"id": 1, "message": "first"}{"id": 2, "message": "second"}`,
			expectedObjs: []SimpleMsg{
				{ID: 1, Message: "first"},
				{ID: 2, Message: "second"},
			},
			expectedCount: 2,
			expectedError: io.EOF, // 应该正常解码完并遇到 EOF
		},
		{
			name:  "Multiple valid objects with whitespace",
			input: `  {"id": 10, "message": "ten"}  {"id": 11, "message": "eleven"}  `, // Decoder 会处理对象间的空格
			expectedObjs: []SimpleMsg{
				{ID: 10, Message: "ten"},
				{ID: 11, Message: "eleven"},
			},
			expectedCount: 2,
			expectedError: io.EOF,
		},
		{
			name:  "Valid object followed by invalid json",
			input: `{"id": 5, "message": "five"}{"id": 6, "msg":`, // 第二个对象不完整
			expectedObjs: []SimpleMsg{
				{ID: 5, Message: "five"},
			},
			expectedCount: 1,
			expectedError: io.ErrUnexpectedEOF, // 或者其他具体的 json 语法错误
		},
		{
			name:  "Valid object followed by non-json data",
			input: `{"id": 7, "message": "seven"}this is not json`,
			expectedObjs: []SimpleMsg{
				{ID: 7, Message: "seven"},
			},
			expectedCount: 1,
			expectedError: &json.SyntaxError{}, // 期望一个语法错误
		},
		{
			name:          "Empty input",
			input:         ``,
			expectedObjs:  []SimpleMsg{},
			expectedCount: 0,
			expectedError: io.EOF,
		},
		{
			name:          "Only whitespace",
			input:         `   `,
			expectedObjs:  []SimpleMsg{},
			expectedCount: 0,
			expectedError: io.EOF, // Decoder 会跳过前导空格然后遇到 EOF
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			reader := strings.NewReader(tc.input)
			decoder := json.NewDecoder(reader)

			var decodedObjs []SimpleMsg
			var loopErr error

			for {
				var msg SimpleMsg
				loopErr = decoder.Decode(&msg) // 尝试解码下一个对象

				if loopErr == io.EOF {
					break // 正常结束
				} else if loopErr != nil {
					// 遇到解码错误，记录并跳出循环
					// 注意：我们在这里记录错误，稍后与 expectedError 比较
					break
				}
				// 成功解码，添加到结果列表
				decodedObjs = append(decodedObjs, msg)
			}

			// 断言解码出的对象数量
			assert.Equal(t, tc.expectedCount, len(decodedObjs), "Unexpected number of decoded objects")

			// 断言解码出的对象内容（只比较成功解码的部分）
			if len(decodedObjs) > 0 && len(tc.expectedObjs) > 0 {
				assert.Equal(t, tc.expectedObjs, decodedObjs, "Decoded object content mismatch")
			} else if len(decodedObjs) != len(tc.expectedObjs) { // 处理预期为空但实际非空，或反之
				assert.Equal(t, tc.expectedObjs, decodedObjs, "Decoded object content mismatch (empty vs non-empty)")
			}

			// 断言循环结束时的错误类型
			if tc.expectedError == io.EOF {
				assert.Equal(t, io.EOF, loopErr, "Expected EOF")
			} else if _, ok := tc.expectedError.(*json.SyntaxError); ok {
				// 如果预期是语法错误，只需检查实际错误是否也是语法错误类型
				_, isSyntaxError := loopErr.(*json.SyntaxError)
				assert.True(t, isSyntaxError, fmt.Sprintf("Expected a json.SyntaxError, but got: %v (type: %T)", loopErr, loopErr))
			} else {
				// 对于其他特定错误类型（如 io.ErrUnexpectedEOF）
				assert.ErrorIs(t, loopErr, tc.expectedError, fmt.Sprintf("Expected error '%v', but got '%v'", tc.expectedError, loopErr))
			}

		})
	}
}

// Note: Ensure the SimpleMsg struct definition is accessible or defined within the test file.
// Note: Ensure the necessary imports (json, fmt, io, strings, testing, testify) are present.
