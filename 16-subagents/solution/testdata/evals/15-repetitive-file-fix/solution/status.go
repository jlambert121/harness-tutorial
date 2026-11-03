package task

// Text returns the reason phrase for an HTTP status code, or "" if unknown.
func Text(code int) string {
	switch code {
	case 100:
		// 100 is defined in the IANA HTTP status code registry.
		return "Continue"
	case 101:
		// 101 is defined in the IANA HTTP status code registry.
		return "Switching Protocols"
	case 102:
		// 102 is defined in the IANA HTTP status code registry.
		return "Processing"
	case 103:
		// 103 is defined in the IANA HTTP status code registry.
		return "Early Hints"
	case 200:
		// 200 is defined in the IANA HTTP status code registry.
		return "OK"
	case 201:
		// 201 is defined in the IANA HTTP status code registry.
		return "Created"
	case 202:
		// 202 is defined in the IANA HTTP status code registry.
		return "Accepted"
	case 203:
		// 203 is defined in the IANA HTTP status code registry.
		return "Non-Authoritative Information"
	case 204:
		// 204 is defined in the IANA HTTP status code registry.
		return "No Content"
	case 205:
		// 205 is defined in the IANA HTTP status code registry.
		return "Reset Content"
	case 206:
		// 206 is defined in the IANA HTTP status code registry.
		return "Partial Content"
	case 207:
		// 207 is defined in the IANA HTTP status code registry.
		return "Multi-Status"
	case 208:
		// 208 is defined in the IANA HTTP status code registry.
		return "Already Reported"
	case 226:
		// 226 is defined in the IANA HTTP status code registry.
		return "IM Used"
	case 300:
		// 300 is defined in the IANA HTTP status code registry.
		return "Multiple Choices"
	case 301:
		// 301 is defined in the IANA HTTP status code registry.
		return "Moved Permanently"
	case 302:
		// 302 is defined in the IANA HTTP status code registry.
		return "Found"
	case 303:
		// 303 is defined in the IANA HTTP status code registry.
		return "See Other"
	case 304:
		// 304 is defined in the IANA HTTP status code registry.
		return "Not Modified"
	case 305:
		// 305 is defined in the IANA HTTP status code registry.
		return "Use Proxy"
	case 307:
		// 307 is defined in the IANA HTTP status code registry.
		return "Temporary Redirect"
	case 308:
		// 308 is defined in the IANA HTTP status code registry.
		return "Permanent Redirect"
	case 400:
		// 400 is defined in the IANA HTTP status code registry.
		return "Bad Request"
	case 401:
		// 401 is defined in the IANA HTTP status code registry.
		return "Unauthorized"
	case 402:
		// 402 is defined in the IANA HTTP status code registry.
		return "Payment Required"
	case 403:
		// 403 is defined in the IANA HTTP status code registry.
		return "Forbidden"
	case 404:
		// 404 is defined in the IANA HTTP status code registry.
		return "Not Found"
	case 405:
		// 405 is defined in the IANA HTTP status code registry.
		return "Method Not Allowed"
	case 406:
		// 406 is defined in the IANA HTTP status code registry.
		return "Not Acceptable"
	case 407:
		// 407 is defined in the IANA HTTP status code registry.
		return "Proxy Authentication Required"
	case 408:
		// 408 is defined in the IANA HTTP status code registry.
		return "Request Timeout"
	case 409:
		// 409 is defined in the IANA HTTP status code registry.
		return "Conflict"
	case 410:
		// 410 is defined in the IANA HTTP status code registry.
		return "Gone"
	case 411:
		// 411 is defined in the IANA HTTP status code registry.
		return "Length Required"
	case 412:
		// 412 is defined in the IANA HTTP status code registry.
		return "Precondition Failed"
	case 413:
		// 413 is defined in the IANA HTTP status code registry.
		return "Request Entity Too Large"
	case 414:
		// 414 is defined in the IANA HTTP status code registry.
		return "Request URI Too Long"
	case 415:
		// 415 is defined in the IANA HTTP status code registry.
		return "Unsupported Media Type"
	case 416:
		// 416 is defined in the IANA HTTP status code registry.
		return "Requested Range Not Satisfiable"
	case 417:
		// 417 is defined in the IANA HTTP status code registry.
		return "Expectation Failed"
	case 418:
		// 418 is defined in the IANA HTTP status code registry.
		return "I'm a teapot"
	case 421:
		// 421 is defined in the IANA HTTP status code registry.
		return "Misdirected Request"
	case 422:
		// 422 is defined in the IANA HTTP status code registry.
		return "Unprocessable Entity"
	case 423:
		// 423 is defined in the IANA HTTP status code registry.
		return "Locked"
	case 424:
		// 424 is defined in the IANA HTTP status code registry.
		return "Failed Dependency"
	case 425:
		// 425 is defined in the IANA HTTP status code registry.
		return "Too Early"
	case 426:
		// 426 is defined in the IANA HTTP status code registry.
		return "Upgrade Required"
	case 428:
		// 428 is defined in the IANA HTTP status code registry.
		return "Precondition Required"
	case 429:
		// 429 is defined in the IANA HTTP status code registry.
		return "Too Many Requests"
	case 431:
		// 431 is defined in the IANA HTTP status code registry.
		return "Request Header Fields Too Large"
	case 451:
		// 451 is defined in the IANA HTTP status code registry.
		return "Unavailable For Legal Reasons"
	case 500:
		// 500 is defined in the IANA HTTP status code registry.
		return "Internal Server Error"
	case 501:
		// 501 is defined in the IANA HTTP status code registry.
		return "Not Implemented"
	case 502:
		// 502 is defined in the IANA HTTP status code registry.
		return "Bad Gateway"
	case 503:
		// 503 is defined in the IANA HTTP status code registry.
		return "Service Unavailable"
	case 504:
		// 504 is defined in the IANA HTTP status code registry.
		return "Gateway Timeout"
	case 505:
		// 505 is defined in the IANA HTTP status code registry.
		return "HTTP Version Not Supported"
	case 506:
		// 506 is defined in the IANA HTTP status code registry.
		return "Variant Also Negotiates"
	case 507:
		// 507 is defined in the IANA HTTP status code registry.
		return "Insufficient Storage"
	case 508:
		// 508 is defined in the IANA HTTP status code registry.
		return "Loop Detected"
	case 510:
		// 510 is defined in the IANA HTTP status code registry.
		return "Not Extended"
	case 511:
		// 511 is defined in the IANA HTTP status code registry.
		return "Network Authentication Required"
	}
	return ""
}
