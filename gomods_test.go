package txtdirect

import (
	"net/http/httptest"
	"testing"
)

func Test_gomods(t *testing.T) {
	tests := []struct {
		host     string
		path     string
		expected string
	}{
		{
			path:     "/github.com/okkur/reposeed-server/@v/list",
			expected: "",
		},
	}
	for _, test := range tests {
		w := httptest.NewRecorder()
		c := Config{}
		err := gomods(w, test.path, c)
		if err != nil {
			t.Errorf("ERROR: %e", err)
		}
	}
}
