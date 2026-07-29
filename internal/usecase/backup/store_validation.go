package backup

import "strings"

// ValidCloudRegion reports whether a provider Region is safe to interpolate
// into an OSS or COS endpoint hostname.
func ValidCloudRegion(region string) bool {
	if region == "" || len(region) > 63 {
		return false
	}
	for _, char := range region {
		if (char < 'a' || char > 'z') &&
			(char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return region[0] != '-' && region[len(region)-1] != '-'
}

// COSBucketHasAPPID reports whether bucket is a full COS Bucket name with its
// numeric APPID suffix.
func COSBucketHasAPPID(bucket string) bool {
	separator := strings.LastIndexByte(bucket, '-')
	if separator <= 0 || separator == len(bucket)-1 {
		return false
	}
	for _, char := range bucket[separator+1:] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
