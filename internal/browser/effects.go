package browser

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Effects is what observably happened because of a gesture (click / enter /
// change / upload / download): the evidence a person uses to tell a stray
// click that did nothing from one that mattered. The recorder derives it at
// Events() time — URLAfter / NewTab from the navigate events that followed the
// gesture, Download from the gesture's own upgraded type, Requests from the
// wire (watchRequests) attributed by time. It lives in events.json only; the
// editable recording.yaml never carries it.
type Effects struct {
	// URLAfter is the top-level URL once the gesture settled, when it changed
	// (RecordedEvent.URL is the URL before). Empty when the page stayed put.
	URLAfter string `json:"url_after,omitempty"`
	// NewTab reports that the gesture opened a tab.
	NewTab bool `json:"new_tab,omitempty"`
	// Download reports that the gesture started a download.
	Download bool `json:"download,omitempty"`
	// Requests counts the XHR/Fetch/Document requests the gesture triggered,
	// by HTTP method: {"GET": 2, "POST": 1}. Method only — see watchRequests.
	Requests map[string]int `json:"requests,omitempty"`
}

// effectsAttributionWindow is how long after a gesture a request may start and
// still be counted as caused by it. Shorter than downloadAttributionWindow: a
// request the gesture caused starts promptly, while a download can take
// seconds to begin. A var so tests on starved runners can widen it.
var effectsAttributionWindow = 1500 * time.Millisecond

// isGesture reports whether an event is something the user did (as opposed to
// a navigation or wait the recorder synthesized from what the page did).
func isGesture(e RecordedEvent) bool {
	switch e.Type {
	case "click", "enter", "change", "upload", "download":
		return true
	}
	return false
}

// attachEffects fills Effects on every gesture in events, in place. URLAfter is
// the last navigate the page performed before the next gesture (a redirect
// chain's final stop), set only when it differs from the URL the gesture was
// made on; NewTab when any of those navigations opened a tab. Each request is
// attributed to the most recent gesture that preceded it, if within
// effectsAttributionWindow — a request outside every window (polling, an
// analytics heartbeat) attaches to nothing.
func attachEffects(events []RecordedEvent, reqs []recordedRequest) {
	for i := range events {
		e := &events[i]
		if !isGesture(*e) {
			continue
		}
		fx := &Effects{Download: e.Type == "download"}
		for j := i + 1; j < len(events) && !isGesture(events[j]); j++ {
			if events[j].Type != "navigate" {
				continue
			}
			if events[j].URL != e.URL {
				fx.URLAfter = events[j].URL
			} else {
				fx.URLAfter = ""
			}
			if events[j].NewTab {
				fx.NewTab = true
			}
		}
		e.Effects = fx
	}
	if len(reqs) == 0 {
		return
	}
	// Gestures in capture order; a request belongs to the latest one at or
	// before its arrival.
	gestures := make([]int, 0, len(events))
	for i, e := range events {
		if isGesture(e) && e.At > 0 {
			gestures = append(gestures, i)
		}
	}
	sort.SliceStable(gestures, func(a, b int) bool { return events[gestures[a]].At < events[gestures[b]].At })
	for _, rq := range reqs {
		at := rq.At.UnixMilli()
		owner := -1
		for _, gi := range gestures {
			if events[gi].At <= at {
				owner = gi
			} else {
				break
			}
		}
		if owner < 0 || at-events[owner].At > effectsAttributionWindow.Milliseconds() {
			continue
		}
		fx := events[owner].Effects
		if fx.Requests == nil {
			fx.Requests = map[string]int{}
		}
		m := rq.Method
		if m == "" {
			m = "GET"
		}
		fx.Requests[m]++
	}
}

// markLikelyNoop flags the TRAILING run of gestures whose Effects show nothing:
// walking back from the last event, a click or enter with no request other
// than GET, no URL change, no new tab and no download is marked; recorder-
// synthesized waits are skipped; the walk stops at the first event that fails
// the test (a typed value, a write, a navigation, a gesture without Effects).
//
// Tail only, by construction: a click that changed nothing and was followed by
// nothing cannot have contributed to the recording's end state, so dropping it
// is safe IF the user agrees. The same click in the middle may be a
// precondition for a later step (expand a panel so a control appears) and is
// left alone. The test keys on the request method, not on the page: "nothing
// visible changed" is not "no side effect" — a like is a POST that leaves the
// URL alone. A site that reads through POST never gets the marker and falls
// back to the goal and the user's confirmation; a client-side toggle the user
// meant is a false positive at the tail — which is why the marker is a
// question, never a deletion.
func markLikelyNoop(events []RecordedEvent) {
	for i := len(events) - 1; i >= 0; i-- {
		e := &events[i]
		if e.Type == "wait" {
			continue
		}
		if (e.Type != "click" && e.Type != "enter") || e.Effects == nil || !e.Effects.isNoop() {
			return
		}
		e.LikelyNoop = true
	}
}

// isNoop reports whether the effects amount to "nothing observable changed":
// no URL change, no new tab, no download, and no request other than GET.
func (fx *Effects) isNoop() bool {
	if fx.URLAfter != "" || fx.NewTab || fx.Download {
		return false
	}
	for m := range fx.Requests {
		if m != "GET" {
			return false
		}
	}
	return true
}

// String renders the effects for the distill trace: "none", or a space-
// separated list such as `url→https://… new_tab GET×2 POST×1`.
func (fx *Effects) String() string {
	if fx == nil {
		return ""
	}
	var parts []string
	if fx.URLAfter != "" {
		parts = append(parts, "url→"+fx.URLAfter)
	}
	if fx.NewTab {
		parts = append(parts, "new_tab")
	}
	if fx.Download {
		parts = append(parts, "download")
	}
	methods := make([]string, 0, len(fx.Requests))
	for m := range fx.Requests {
		methods = append(methods, m)
	}
	sort.Strings(methods)
	for _, m := range methods {
		parts = append(parts, fmt.Sprintf("%s×%d", m, fx.Requests[m]))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " ")
}
