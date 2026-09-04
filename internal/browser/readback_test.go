package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A login form the browser has autofilled: the digest must report the values
// separately from the labels, withhold the password, and FieldState must show
// TypeText appending rather than replacing.
const readbackFixture = `<!doctype html><html><body>
	<input name="user" placeholder="Username" value="alice">
	<input name="pw" type="password" placeholder="Password" value="correct horse">
	<input name="empty" placeholder="Nickname">
	<textarea name="bio">hello</textarea>
	<input type="submit" value="Sign in">
	<button id="go">Go</button>
	<div id="rich" contenteditable="true">rich</div>
</body></html>`

func TestFieldStateAndDigest_PrefilledLogin(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(readbackFixture))
	}))
	defer srv.Close()

	b := newBrowser(t, ctx)
	defer b.Close()
	page, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	if err := page.Navigate(ctx, srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if err := page.WaitFor(ctx, "#go", testWaitTimeout); err != nil {
		t.Fatalf("wait: %v", err)
	}

	// Digest: labels and values are separate; password content withheld;
	// button-like inputs keep their value as text.
	digest, err := InteractiveDigest(ctx, page, "", 60)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	bySel := map[string]DigestElement{}
	for _, e := range digest {
		bySel[e.Selector] = e
	}
	user := bySel[`input[name="user"]`]
	if user.Text != "Username" || user.Value != "alice" || user.ValueLen != 5 || user.Password {
		t.Errorf("user field = %+v, want text Username / value alice", user)
	}
	pw := bySel[`input[name="pw"]`]
	if pw.Text != "Password" || pw.Value != "" || pw.ValueLen != len("correct horse") || !pw.Password {
		t.Errorf("password field = %+v, want value withheld with length %d", pw, len("correct horse"))
	}
	empty := bySel[`input[name="empty"]`]
	if empty.Text != "Nickname" || empty.Value != "" || empty.ValueLen != 0 {
		t.Errorf("empty field = %+v, want placeholder text and no value", empty)
	}
	bio := bySel[`textarea[name="bio"]`]
	if bio.Text != "bio" || bio.Value != "hello" {
		t.Errorf("textarea = %+v, want name as text and hello as value", bio)
	}
	var submit DigestElement
	for _, e := range digest {
		if e.Text == "Sign in" {
			submit = e
		}
	}
	if submit.Selector == "" || submit.Value != "" {
		t.Errorf("submit button should keep its value as text and report no field value: %+v", submit)
	}

	// FieldState: reading back after a type on a prefilled field shows old+new.
	if err := page.TypeText(ctx, `input[name="user"]`, "alice"); err != nil {
		t.Fatalf("type: %v", err)
	}
	st := page.FieldState(ctx, `input[name="user"]`)
	if !st.Found || st.Value != "alicealice" || st.ValueLen != 10 || st.Password {
		t.Errorf("FieldState after type = %+v, want alicealice", st)
	}
	st = page.FieldState(ctx, `input[name="pw"]`)
	if !st.Found || !st.Password || st.Value != "" || st.ValueLen != len("correct horse") {
		t.Errorf("FieldState password = %+v, want withheld value with length", st)
	}
	st = page.FieldState(ctx, "#rich")
	if !st.Found || st.Value != "rich" {
		t.Errorf("FieldState contenteditable = %+v, want textContent rich", st)
	}
	if st := page.FieldState(ctx, "#nope"); st.Found {
		t.Errorf("FieldState on a missing selector should report Found=false: %+v", st)
	}
}
