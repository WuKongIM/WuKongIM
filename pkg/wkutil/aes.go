package wkutil

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
)

// AesEncryptSimple 加密
func AesEncryptSimple(origData []byte, key string, iv string) ([]byte, error) {
	return AesDecryptPkcs5(origData, []byte(key), []byte(iv))
}

// AesEncryptPkcs5 加密
func AesEncryptPkcs5(origData []byte, key []byte, iv []byte) ([]byte, error) {
	return AesEncrypt(origData, key, iv, PKCS5Padding)
}

// AesEncryptPkcs7 加密
func AesEncryptPkcs7(origData []byte, key []byte, iv []byte) ([]byte, error) {
	return AesEncrypt(origData, key, iv, PKCS7Padding)
}

// AesEncryptPkcs7Base64
func AesEncryptPkcs7Base64(origData []byte, key []byte, iv []byte) ([]byte, error) {
	data, err := AesEncrypt(origData, key, iv, PKCS7Padding)
	if err != nil {
		return data, err
	}
	dataStr := base64.StdEncoding.EncodeToString(data)
	return []byte(dataStr), nil
}

// AesEncrypt AesEncrypt
func AesEncrypt(origData []byte, key []byte, iv []byte, paddingFunc func([]byte, int) []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	blockSize := block.BlockSize()
	origData = paddingFunc(origData, blockSize)

	blockMode := cipher.NewCBCEncrypter(block, iv)
	crypted := make([]byte, len(origData))
	blockMode.CryptBlocks(crypted, origData)
	return crypted, nil
}

// AesDecryptSimple 解密
func AesDecryptSimple(crypted []byte, key string, iv string) ([]byte, error) {
	return AesDecryptPkcs5(crypted, []byte(key), []byte(iv))
}

// AesDecryptPkcs5 解密
func AesDecryptPkcs5(crypted []byte, key []byte, iv []byte) ([]byte, error) {
	return AesDecrypt(crypted, key, iv, PKCS5UnPadding)
}

// AesDecryptPkcs7 解密
func AesDecryptPkcs7(crypted []byte, key []byte, iv []byte) ([]byte, error) {
	return AesDecrypt(crypted, key, iv, PKCS7UnPadding)
}

// AesDecryptPkcs7Base64 解密
func AesDecryptPkcs7Base64(crypted []byte, key []byte, iv []byte) ([]byte, error) {
	cryptedData, err := base64.StdEncoding.DecodeString(string(crypted))
	if err != nil {
		return nil, err
	}
	return AesDecrypt(cryptedData, key, iv, PKCS7UnPadding)
}

// AesDecrypt AesDecrypt
func AesDecrypt(crypted, key []byte, iv []byte, unPaddingFunc func([]byte) []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	blockMode := cipher.NewCBCDecrypter(block, iv)
	origData := make([]byte, len(crypted))
	blockMode.CryptBlocks(origData, crypted)
	origData = unPaddingFunc(origData)
	return origData, nil
}

// PKCS5Padding PKCS5Padding
func PKCS5Padding(ciphertext []byte, blockSize int) []byte {
	padding := blockSize - len(ciphertext)%blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(ciphertext, padtext...)
}

// PKCS5UnPadding PKCS5UnPadding
func PKCS5UnPadding(origData []byte) []byte {
	length := len(origData)
	unpadding := int(origData[length-1])
	if length < unpadding {
		return []byte("unpadding error")
	}
	return origData[:(length - unpadding)]
}

// PKCS7Padding PKCS7Padding
func PKCS7Padding(ciphertext []byte, blockSize int) []byte {
	padding := blockSize - len(ciphertext)%blockSize // 需要填充的数目
	// 只要少于256就能放到一个byte中，默认的blockSize=16(即采用16*8=128, AES-128长的密钥)

	// 最少填充1个byte，如果原文刚好是blocksize的整数倍，则再填充一个blocksize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding) // 生成填充的文本

	return append(ciphertext, padtext...)
}

// PKCS7UnPadding PKCS7UnPadding
func PKCS7UnPadding(origData []byte) []byte {
	length := len(origData)

	unpadding := int(origData[length-1])

	return origData[:(length - unpadding)]

}
