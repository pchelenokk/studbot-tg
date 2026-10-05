package bot

import (
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// The wizard maps are shared by the goroutine that handles each update, so the
// helpers must stay race free. Run with -race to make this meaningful.
func TestWizardMapsAreRaceFree(t *testing.T) {
	b := &Bot{
		wizards:   make(map[int64]*Wizard),
		userLocks: make(map[int64]*sync.Mutex),
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			lock := b.userLock(id)
			lock.Lock()
			b.putWizard(id, &Wizard{Flow: "book", Await: StepBookTitle, Data: map[string]string{"title": "t"}})
			w := b.getWizard(id)
			if w == nil {
				t.Errorf("wizard for %d disappeared", id)
			}
			b.delWizard(id)
			lock.Unlock()
		}(int64(i))
	}
	wg.Wait()

	if b.getWizard(0) != nil {
		t.Error("wizard must be removed")
	}
}

func TestSplitCallbackKnownButtons(t *testing.T) {
	cases := []struct {
		data   string
		action string
		arg    string
	}{
		{"menu", "menu", ""},
		{"books", "books", ""},
		{"homework", "homework", ""},
		{"students", "students", ""},
		// admin_stats must not be swallowed by the shorter "admin" prefix.
		{"admin_stats", "admin_stats", ""},
		{"admin", "admin", ""},
		// subject filter carries an index, so the prefix must resolve.
		{"hw_filter_0", "hw_filter", "0"},
		{"hw_filter_12", "hw_filter", "12"},
		{"book_open_5", "book_open", "5"},
		{"hw_del_3", "hw_del", "3"},
		{"vote_3_0", "vote", "3_0"},
		{"rvote_3_1", "rvote", "3_1"},
		{"checkin_7_present", "checkin", "7_present"},
		{"setrole_99_proforg", "setrole", "99_proforg"},
	}

	for _, c := range cases {
		action, arg := splitCallback(c.data)
		if action != c.action || arg != c.arg {
			t.Errorf("splitCallback(%q) = (%q, %q), want (%q, %q)", c.data, action, arg, c.action, c.arg)
		}
	}
}

// Every action handled by the callback switch must be reachable, i.e. no other
// entry may shadow it as a prefix.
func TestCallbackActionsDoNotShadowEachOther(t *testing.T) {
	for _, action := range callbackActions {
		got, arg := splitCallback(action + "_1")
		if got != action {
			t.Errorf("action %q is shadowed: splitCallback(%q) = %q", action, action+"_1", got)
		}
		if arg != "1" {
			t.Errorf("action %q: arg = %q, want \"1\"", action, arg)
		}
	}
}

func TestEscHTML(t *testing.T) {
	got := escHTML(`Учебник "Физика" <b>&</b>`)
	want := "Учебник &quot;Физика&quot; &lt;b&gt;&amp;&lt;/b&gt;"
	if got != want {
		t.Errorf("escHTML() = %q, want %q", got, want)
	}
}

// fit must never cut a multi-byte rune in half (the old byte slicing produced
// invalid UTF-8 for Cyrillic titles).
func TestFitKeepsValidUTF8(t *testing.T) {
	titles := []string{
		"Математика 1 курс задачник",
		"Методические Указания",
		`Учебник "Английский для инженеров"`,
		"short",
	}
	for _, s := range titles {
		got := fit(s, 30)
		if !utf8.ValidString(got) {
			t.Errorf("fit(%q, 30) = %q is not valid UTF-8", s, got)
		}
		if utf8.RuneCountInString(s) > 30 && !strings.HasSuffix(got, "…") {
			t.Errorf("fit(%q, 30) = %q, want an ellipsis suffix", s, got)
		}
		if utf8.RuneCountInString(got) > 31 {
			t.Errorf("fit(%q, 30) = %q is too long", s, got)
		}
	}
}

func TestFitShortStringsUnchanged(t *testing.T) {
	if got := fit("Матан", 30); got != "Матан" {
		t.Errorf("fit() = %q, want unchanged", got)
	}
}
