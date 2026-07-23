package hostexpansion

import (
	"container/list"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ExpandHosts takes an abbreviated host string and unpacks it into a list of hostnames
// contained within the abbreviation.
//
// Example:
// - ExpandHosts("host[1..2]") == []string{"host1", "host2"}
func ExpandHosts(input string) []string {
	// Linked list acting as a simple FIFO stack for tracking work items as we unpack different abbreviation depths
	q := list.New()
	q.PushBack(input)

	var returnHosts []string

	for e := q.Front(); e != nil; e = e.Next() {
		var hosts []string
		if !strings.Contains(input, "]") {
			hosts = []string{input}
		} else {
			hosts = processQueueItem(e.Value.(string), q)
		}
		if len(hosts) > 0 {
			returnHosts = append(returnHosts, hosts...)
		}
	}

	return returnHosts
}

func processQueueItem(host string, q *list.List) []string {
	var abbrOpen bool
	var prefix string
	var abbr string
	for idx, char := range host {
		switch char {
		case '[':
			abbrOpen = true
			prefix = host[:idx]
		case ']':
			if !abbrOpen {
				return []string{fmt.Sprintf("invalid-host:%s", host)}
			}
			return expandAbbreviation(prefix, host[len(prefix)+1:idx], host[idx+1:], q)
		default:
			if abbrOpen {
				abbr += string(char)
			} else {
				prefix += string(char)
			}
		}
	}
	return []string{}
}

func expandAbbreviation(prefix, abbr, suffix string, q *list.List) []string {
	parts := strings.Split(abbr, "..")
	if len(parts) < 2 {
		return []string{prefix + abbr, suffix}
	}
	numericParts := make([]int, len(parts))
	for idx, part := range parts {
		numericParts[idx], _ = strconv.Atoi(part)
	}
	start := slices.Min(numericParts)
	end := slices.Max(numericParts)

	var returnVal []string
	for i := 0; i <= end-start; i++ {
		if strings.Contains(suffix, "[") {
			q.PushBack(prefix + strconv.Itoa(i+start) + suffix)
		} else {
			returnVal = append(returnVal, prefix+strconv.Itoa(start+i)+suffix)
		}
	}

	return returnVal
}
