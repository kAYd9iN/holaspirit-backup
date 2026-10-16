package compare

import (
	"fmt"
	"strings"
	"time"
)

// hsSet is the Holaspirit backup, as written by v0.5.0 of this tool.
type hsSet struct {
	time                   time.Time
	members                []obj
	memberByID             map[string]obj
	circles                []obj
	circleByID             map[string]obj
	roles                  []obj
	roleByID               map[string]obj // roles.json plus linked roles
	acc, dom, assigned     map[string]obj
	policies, okrs         []obj
	krByID                 map[string]obj
	tensions, meetings     []obj
	tasks, boards, columns []obj
	labels, timeframes     []obj
	customFields, rolesCF  []obj
	counts                 map[string]int    // record counts of files compared only by count
	meetingTemplates       map[string]string // id → name
}

// tsSet is the Talkspirit backup.
type tsSet struct {
	time                       time.Time
	users                      []obj
	userByID                   map[string]obj
	circles, roles             []obj
	circleByID                 map[string]obj
	roleTemplates, memberships []obj
	tensions, meetings         []obj
	agenda                     map[string][]obj
	tasks                      []obj // top-level and sub-tasks
	projects                   []obj
	sections, projLabels       map[string][]obj
	goals, timePeriods         []obj
	keyResults                 map[string][]obj
	documents                  []obj
	docContent                 map[string][]obj
	cfCircle, cfRole, cfUser   []obj
	auditLogs                  []obj
	have                       map[string]bool // which files exist
}

func manifestTime(dir string) time.Time {
	var m struct {
		Timestamp string `json:"timestamp"`
	}
	if ok, err := readJSON(dir, "backup-manifest", &m); ok && err == nil {
		return parseTime(m.Timestamp)
	}
	return time.Time{}
}

// loader collects the first error of a series of loads.
type loader struct {
	dir string
	err error
}

func (l *loader) arr(name string) []obj {
	if l.err != nil {
		return nil
	}
	out, _, err := loadArr(l.dir, name)
	if err != nil {
		l.err = err
	}
	return out
}

func (l *loader) linked(name string) map[string][]obj {
	if l.err != nil {
		return nil
	}
	out, err := loadLinked(l.dir, name)
	if err != nil {
		l.err = err
	}
	return out
}

func loadHS(dir string) (*hsSet, error) {
	if !exists(dir, "roles") || !exists(dir, "circles") {
		return nil, fmt.Errorf("%s does not look like a Holaspirit backup (roles.json/circles.json missing)", dir)
	}
	l := &loader{dir: dir}
	h := &hsSet{time: manifestTime(dir), counts: map[string]int{}}
	h.members = l.arr("members")
	h.circles = l.arr("circles")
	h.roles = l.arr("roles")
	rl := l.linked("roles")
	h.policies = l.arr("policies")
	h.okrs = l.arr("okrs")
	ol := l.linked("okrs")
	h.tensions = l.arr("tensions")
	h.meetings = l.arr("meetings")
	h.tasks = l.arr("tasks")
	h.boards = l.arr("boards")
	h.columns = l.arr("columns")
	h.labels = l.arr("labels")
	h.timeframes = l.arr("timeframes")
	h.customFields = l.arr("custom-fields")
	h.rolesCF = l.arr("roles-custom-fields")
	h.meetingTemplates = map[string]string{}
	for _, t := range l.arr("meetingtemplates") {
		h.meetingTemplates[str(t, "id")] = str(t, "name")
	}
	for _, n := range []string{"checklists", "metrics", "publications", "publications-revision", "attachments",
		"calendars", "highlights", "meetingtemplates", "meetings-recurrences", "webhooks", "templates", "comment-feeds"} {
		h.counts[n] = len(l.arr(n))
	}
	if l.err != nil {
		return nil, l.err
	}
	h.memberByID = byID(h.members)
	h.circleByID = byID(h.circles)
	h.roleByID = byID(rl["roles"])
	for id, r := range byID(h.roles) {
		h.roleByID[id] = r
	}
	h.acc = byID(rl["accountabilities"])
	h.dom = byID(rl["domains"])
	h.assigned = byID(rl["assignedmembers"])
	h.krByID = byID(ol["keyresults"])
	return h, nil
}

func loadTS(dir string) (*tsSet, error) {
	if !exists(dir, "whoami") || !exists(dir, "roles") {
		return nil, fmt.Errorf("%s does not look like a Talkspirit backup (whoami.json/roles.json missing)", dir)
	}
	l := &loader{dir: dir}
	t := &tsSet{time: manifestTime(dir), have: map[string]bool{}}
	t.users = l.arr("users")
	t.circles = l.arr("circles")
	t.roles = l.arr("roles")
	t.roleTemplates = l.arr("role-templates")
	t.memberships = l.arr("memberships")
	t.tensions = l.arr("tensions")
	t.meetings = l.arr("meetings")
	t.tasks = l.arr("tasks")
	t.projects = l.arr("projects")
	t.goals = l.arr("goals")
	t.timePeriods = l.arr("time-periods")
	t.documents = l.arr("documents")
	t.cfCircle = l.arr("custom-fields-circle")
	t.cfRole = l.arr("custom-fields-role")
	t.cfUser = l.arr("custom-fields-user")
	t.auditLogs = l.arr("audit-logs")
	if l.err != nil {
		return nil, l.err
	}
	children := map[string]*map[string][]obj{
		"meeting-agenda-items": &t.agenda, "sections": &t.sections, "labels": &t.projLabels,
		"goal-key-results": &t.keyResults, "document-content": &t.docContent,
	}
	for name, dst := range children {
		m, ok, err := loadChildren(dir, name)
		if err != nil {
			return nil, err
		}
		t.have[name] = ok
		if m == nil {
			m = map[string][]obj{}
		}
		*dst = m
	}
	sub, ok, err := loadChildren(dir, "subtasks")
	if err != nil {
		return nil, err
	}
	t.have["subtasks"] = ok
	for _, list := range sub {
		t.tasks = append(t.tasks, list...)
	}
	for _, n := range []string{"audit-logs", "documents", "tensions", "meetings", "tasks", "goals", "projects"} {
		t.have[n] = exists(dir, n)
	}
	t.userByID = byID(t.users)
	t.circleByID = byID(t.circles)
	return t, nil
}

func exists(dir, name string) bool {
	var v any
	ok, err := readJSON(dir, name, &v)
	return ok && err == nil
}

// paths builds "A › B › C" name paths of a parent-linked tree.
func paths(byID map[string]obj, parentKey string, name func(obj) string) map[string]string {
	out := map[string]string{}
	var walk func(id string, depth int) string
	walk = func(id string, depth int) string {
		if p, ok := out[id]; ok {
			return p
		}
		o, ok := byID[id]
		if !ok || depth > 50 {
			return ""
		}
		p := name(o)
		if parent := str(o, parentKey); parent != "" && parent != id {
			if pp := walk(parent, depth+1); pp != "" {
				p = pp + " › " + p
			}
		}
		out[id] = p
		return p
	}
	for id := range byID {
		walk(id, 0)
	}
	return out
}

func normPath(p string) string {
	parts := strings.Split(p, " › ")
	for i, s := range parts {
		parts[i] = norm(s)
	}
	return strings.Join(parts, "/")
}
