package formats

import "strings"

// Date reports whether value is an RFC 3339 full-date, including leap-year rules.
func Date(value string) bool {
	if len(value) != 10 || value[4] != '-' || value[7] != '-' {
		return false
	}
	for i := range len(value) {
		if i == 4 || i == 7 {
			continue
		}
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	year := int(value[0]-'0')*1000 + int(value[1]-'0')*100 + int(value[2]-'0')*10 + int(value[3]-'0')
	month := int(value[5]-'0')*10 + int(value[6]-'0')
	day := int(value[8]-'0')*10 + int(value[9]-'0')
	days := [13]int{0, 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	if month < 1 || month > 12 || day < 1 {
		return false
	}
	max := days[month]
	if month == 2 && year%4 == 0 && (year%100 != 0 || year%400 == 0) {
		max = 29
	}
	return day <= max
}

// Time reports whether value is an RFC 3339 full-time. A timezone is required;
// leap seconds must normalize to 23:59:60 UTC.
func Time(value string) bool {
	if len(value) < 9 || value[2] != ':' || value[5] != ':' {
		return false
	}
	for _, i := range []int{0, 1, 3, 4, 6, 7} {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	hour := int(value[0]-'0')*10 + int(value[1]-'0')
	minute := int(value[3]-'0')*10 + int(value[4]-'0')
	second := int(value[6]-'0')*10 + int(value[7]-'0')
	if hour > 23 || minute > 59 || second > 60 {
		return false
	}
	pos := 8
	if value[pos] == '.' {
		pos++
		start := pos
		for pos < len(value) && value[pos] >= '0' && value[pos] <= '9' {
			pos++
		}
		if pos == start || pos == len(value) {
			return false
		}
	}
	offset := 0
	if value[pos] == 'Z' || value[pos] == 'z' {
		if pos+1 != len(value) {
			return false
		}
	} else {
		if len(value)-pos != 6 || (value[pos] != '+' && value[pos] != '-') || value[pos+3] != ':' {
			return false
		}
		for _, i := range []int{pos + 1, pos + 2, pos + 4, pos + 5} {
			if value[i] < '0' || value[i] > '9' {
				return false
			}
		}
		h := int(value[pos+1]-'0')*10 + int(value[pos+2]-'0')
		m := int(value[pos+4]-'0')*10 + int(value[pos+5]-'0')
		if h > 23 || m > 59 {
			return false
		}
		offset = h*60 + m
		if value[pos] == '-' {
			offset = -offset
		}
	}
	return second < 60 || ((hour*60+minute-offset)%1440+1440)%1440 == 1439
}

// DateTime reports whether value is an RFC 3339 date and time separated by T or t.
func DateTime(value string) bool {
	i := strings.IndexAny(value, "Tt")
	return i >= 0 && Date(value[:i]) && Time(value[i+1:])
}

var durationPattern = mustPattern("^P((\\d+Y(\\d+M(\\d+D)?)?|\\d+M(\\d+D)?|\\d+D)(T(\\d+H(\\d+M(\\d+S)?)?|\\d+M(\\d+S)?|\\d+S))?|T(\\d+H(\\d+M(\\d+S)?)?|\\d+M(\\d+S)?|\\d+S)|\\d+W)$")

// Duration reports whether value is an ISO 8601 duration.
func Duration(value string) bool { return durationPattern.MatchString(value) }
