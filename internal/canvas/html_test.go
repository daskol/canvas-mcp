package canvas

import "testing"

func TestPlainText(t *testing.T) {
	source := `<h2>Week &amp; topics</h2><p>Read <a href="/files/7">these notes</a>.</p><ul><li>Первое</li><li>Second<br>line</li></ul><script>ignore me</script><style>ignore me too</style><p><img alt="A diagram"></p>`
	want := "Week & topics\nRead these notes (/files/7).\nПервое\nSecond\nline\nA diagram"
	if got := plainText(source); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	data := map[string]any{"items": []any{map[string]any{"body": source}}, "description": nil}
	addPlainText(data)
	item := data["items"].([]any)[0].(map[string]any)
	if item["body"] != source || item["body_text"] != want {
		t.Fatal("nested HTML was lost or not converted")
	}
	if _, exists := data["description_text"]; exists {
		t.Fatal("invented text for null content")
	}
}
