package helper

import "strings"

// ExtractName returns the suffix after the final '/'.
//
// It returns the input unchanged when '/' is not present.
func ExtractName(arn string) string {
	idx := strings.LastIndex(arn, "/")
	if idx == -1 {
		return arn
	}

	return arn[idx+1:]
}

// ExtractImageName returns the image name suffix after the final '/'.
//
// It keeps tags and digests intact (for example, repo/app:tag).
func ExtractImageName(image string) string {
	idx := strings.LastIndex(image, "/")
	if idx == -1 {
		return image
	}

	return image[idx+1:]
}
