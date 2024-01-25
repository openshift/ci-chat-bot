package utils

import "strings"

// SanitizeLabels ensures that the mapping will be valid as k8s metadata.labels
func SanitizeLabels(input map[string]string) map[string]string {
	return mungeLabels(trimLabels(input))
}

func isAlpha(b uint8) bool {
	return ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z') || ('0' <= b && b <= '9')
}

func isAlphaOrOthers(b uint8) bool {
	return isAlpha(b) || b == '.' || b == '_' || b == '-'
}

// mungeLabels ensures that label values don't contain invalid characters
func mungeLabels(labels map[string]string) map[string]string {
	output := map[string]string{}
	for key, value := range labels {
		if len(value) == 0 {
			output[key] = value
			continue
		}
		munged := strings.Builder{}
		if isAlpha(value[0]) {
			munged.WriteByte(value[0])
		}

		for i := 1; i < len(value)-1; i++ {
			b := value[i]
			if isAlphaOrOthers(b) {
				munged.WriteByte(b)
			} else {
				munged.WriteString("_")
			}
		}

		if len(value) > 1 && isAlpha(value[len(value)-1]) {
			munged.WriteByte(value[len(value)-1])
		}

		output[key] = munged.String()
	}
	return output
}

// trimLabels ensures that all label values are less than 64 characters
// in length and thus valid.
func trimLabels(labels map[string]string) map[string]string {
	for k, v := range labels {
		labels[k] = Trim63(v)
	}
	return labels
}

// Trim63 ensure that the value is less than 64 characters in length
func Trim63(value string) string {
	if len(value) > 63 {
		return value[:60] + "xxx"
	}
	return value
}
