package main

import "testing"

func TestPrettyXML(t *testing.T) {
	got := prettyXML("application/xml", `<a><b>hi</b><c/></a>`)
	want := "<a>\n  <b>hi</b>\n  <c/>\n</a>"
	if got != want {
		t.Fatalf("pretty = %q", got)
	}

	soap := prettyXML("application/soap+xml", `<?xml version="1.0" encoding="UTF-8"?>`+"\n"+
		`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><n>1</n></soap:Body></soap:Envelope>`)
	wantSOAP := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" +
		"<soap:Envelope xmlns:soap=\"http://schemas.xmlsoap.org/soap/envelope/\">\n" +
		"  <soap:Body>\n" +
		"    <n>1</n>\n" +
		"  </soap:Body>\n" +
		"</soap:Envelope>"
	if soap != wantSOAP {
		t.Fatalf("soap = %q", soap)
	}

	if prettyXML("text/plain", "<a b='c'>x</a>") != "<a b='c'>x</a>" {
		t.Fatalf("quotes changed: %q", prettyXML("text/plain", "<a b='c'>x</a>"))
	}
	if prettyXML("text/plain", "<!-- hi --><root/>") != "<!-- hi -->\n<root/>" {
		t.Fatalf("comment = %q", prettyXML("text/plain", "<!-- hi --><root/>"))
	}
	cdata := prettyXML("application/xml", `<root><![CDATA[ <a> ]]></root>`)
	if cdata != "<root>\n  <![CDATA[ <a> ]]>\n</root>" {
		t.Fatalf("cdata = %q", cdata)
	}
	if prettyXML("text/plain", "hello") != "" {
		t.Fatal("plain text was pretty-printed")
	}
	if prettyXML("application/json", "<a/>") != "" {
		t.Fatal("json content type was pretty-printed as xml")
	}
	if prettyXML("text/html", "<p>hi</p>") != "" {
		t.Fatal("html was pretty-printed as xml")
	}
	if prettyXML("application/xml", "<a>") != "" {
		t.Fatal("invalid xml was pretty-printed")
	}
	if prettyXML("application/xml", "<a></b>") != "" {
		t.Fatal("mismatched xml was pretty-printed")
	}
	quoted := prettyXML("text/xml", `<a b="x>y">z</a>`)
	if quoted != `<a b="x>y">z</a>` {
		t.Fatalf("quoted gt = %q", quoted)
	}

	view := formatBody("text/plain", `<a><b>hi</b></a>`)
	if view.Kind != "xml" || view.Label != "Pretty XML" || view.Class != "xml" || view.Pretty == "" {
		t.Fatalf("format = %+v", view)
	}
	jsonView := formatBody("application/json", `{"n":1}`)
	if jsonView.Kind != "json" || jsonView.Label != "Pretty JSON" {
		t.Fatalf("json format = %+v", jsonView)
	}
	if formatBody("text/html", "<p>hi</p>").Class != "" {
		t.Fatal("html was marked for xml highlighting")
	}
	broken := formatBody("application/xml", "<a>")
	if broken.Pretty != "" || broken.Class != "xml" {
		t.Fatalf("broken xml view = %+v", broken)
	}
}
