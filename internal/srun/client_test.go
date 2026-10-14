package srun

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"srunkeeper/internal/config"
)

func TestLoginProtocolAndOnlineCheck(t *testing.T) {
	online := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("ip") != "192.168.1.2" {
			t.Error("IP missing")
		}
		switch r.URL.Path {
		case "/cgi-bin/rad_user_info":
			if online {
				fmt.Fprint(w, `jQuery({"error":"ok","online_ip":"192.168.1.2"});`)
			} else {
				fmt.Fprint(w, `{"error":"not_online_error"}`)
			}
		case "/cgi-bin/get_challenge":
			fmt.Fprint(w, `jQuery({"error":"ok","challenge":"0123456789abcdef0123456789abcdef"})`)
		case "/cgi-bin/srun_portal":
			token := "0123456789abcdef0123456789abcdef"
			mac := hmac.New(md5.New, []byte(token))
			mac.Write([]byte("secret"))
			digest := fmt.Sprintf("%x", mac.Sum(nil))
			if q.Get("password") != "{MD5}"+digest || q.Get("username") != "student" || q.Get("ac_id") != "7" || q.Get("action") != "login" {
				t.Error("incorrect login parameters")
			}
			info := q.Get("info")
			if !strings.HasPrefix(info, "{SRBX1}") {
				t.Error("missing info marker")
			}
			sum := fmt.Sprintf("%x", sha1.Sum([]byte(token+"student"+token+digest+token+"7"+token+"192.168.1.2"+token+"200"+token+"1"+token+info)))
			if q.Get("chksum") != sum {
				t.Error("checksum mismatch")
			}
			online = true
			fmt.Fprint(w, `jQuery({"error":"ok","ecode":0})`)
		default:
			t.Error("unexpected path")
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	s := config.Defaults().School
	s.Host = srv.URL
	s.ACID = "7"
	c := New(s)
	if ok, err := c.Check(context.Background(), "192.168.1.2"); err != nil || ok {
		t.Fatalf("offline: %v %v", ok, err)
	}
	if err := c.Login(context.Background(), "student", "secret", "192.168.1.2"); err != nil {
		t.Fatal(err)
	}
	if ok, err := c.Check(context.Background(), "192.168.1.2"); err != nil || !ok {
		t.Fatalf("online: %v %v", ok, err)
	}
}
func TestMalformedResponsesAndNumericCodes(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
		want   string
	}{{`<html>portal</html>`, 200, "非预期 JSON"}, {`jQuery({"error":"ok"})`, 503, "HTTP 503"}, {`jQuery({"error":"server_error"})`, 200, "无法确认"}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
		s := config.Defaults().School
		s.Host = srv.URL
		c := New(s)
		_, err := c.Check(context.Background(), "192.168.1.2")
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("unexpected error %v", err)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "get_challenge") {
			fmt.Fprint(w, `{"challenge":"1234567890123456"}`)
		} else {
			fmt.Fprint(w, `{"error":"login_error","ecode":123}`)
		}
	}))
	defer srv.Close()
	s := config.Defaults().School
	s.Host = srv.URL
	if err := New(s).Login(context.Background(), "user", "secret", "192.168.1.2"); err == nil || !strings.Contains(err.Error(), "123") {
		t.Fatalf("numeric code lost: %v", err)
	}
}
func TestCancelledRequestDoesNotExposeSecrets(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := config.Defaults().School
	s.Host = "http://127.0.0.1:1"
	_, err := New(s).get(ctx, "/cgi-bin/srun_portal", map[string][]string{"info": {"super-secret"}})
	if err == nil || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("unsafe error %v", err)
	}
}
