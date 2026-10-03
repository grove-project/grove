package runtime

import "testing"

func TestApplicationRoutesComeFromComponentRegistration(t *testing.T) {
	previous := activeApplication
	t.Cleanup(func() { activeApplication = previous })
	activeApplication = Definition{Components: []Component{
		{Name: "Web", Routes: []Route{{Method: "GET", Path: "/"}}},
		{Name: "Orders"},
	}}
	result, err := newApplicationController("", "").appIngress(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	view := result.(applicationIngressView)
	if len(view.Routes) != 1 || view.Routes[0] != (applicationRouteView{Method: "GET", Path: "/", Service: "Web"}) {
		t.Fatalf("routes = %#v; want GET / -> Web", view.Routes)
	}
}
