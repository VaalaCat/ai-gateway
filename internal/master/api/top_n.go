package api

// ParseTopN normalizes the shared page-level chart ranking limit.
func ParseTopN(raw int) (int, error) {
	if raw == 0 {
		return 5, nil
	}
	switch raw {
	case 5, 10, 20:
		return raw, nil
	default:
		return 0, ErrorWithCode(400, "InvalidTopN", "top_n must be one of 5, 10, 20", nil)
	}
}
