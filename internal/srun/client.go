// Protocol adapted from Unukalhai727/srun-login (README declares MIT).
package srun

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"srunkeeper/internal/config"
)

const alphabet = "LVoJPiCN2R8G90yg+hmFHuacZ1OWMnrsSTXkYpUq/3dlbfKwv6xztjI7DeBE45QA"

type Client struct {
	school config.School
	http   *http.Client
}

func (c *Client) Close() { c.http.CloseIdleConnections() }
func New(s config.School) *Client {
	// Avoid sending campus credentials through an environment-configured proxy.
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return &Client{s, &http.Client{Timeout: 10 * time.Second, Transport: t, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *Client) get(ctx context.Context, path string, q url.Values) (map[string]json.RawMessage, error) {
	q.Set("callback", "jQuery")
	q.Set("_", strconv.FormatInt(time.Now().UnixMilli(), 10))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.school.Host, "/")+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, errors.New("无法构造认证请求")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// url.Error contains the full URL, including sensitive login query parameters.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("认证服务器连接失败或超时，请检查校园网连接和学校地址")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("认证服务器返回 HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return nil, errors.New("读取认证响应失败")
	}
	if len(b) > 1<<20 {
		return nil, errors.New("认证响应过大")
	}
	s := strings.TrimSpace(string(b))
	if strings.HasPrefix(s, "jQuery(") {
		s = strings.TrimSpace(strings.TrimSuffix(s, ";"))
		if !strings.HasSuffix(s, ")") {
			return nil, errors.New("认证 JSONP 格式错误")
		}
		s = strings.TrimSpace(s[len("jQuery(") : len(s)-1])
	}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(s), &m) != nil || m == nil {
		return nil, errors.New("认证服务器返回了非预期 JSON，请检查学校配置")
	}
	return m, nil
}
func field(m map[string]json.RawMessage, k string) string {
	b := m[k]
	var s string
	if json.Unmarshal(b, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(b, &n) == nil {
		return n.String()
	}
	return ""
}
func (c *Client) Check(ctx context.Context, ip string) (bool, error) {
	m, err := c.get(ctx, "/cgi-bin/rad_user_info", url.Values{"ip": {ip}})
	if err != nil {
		return false, err
	}
	e := field(m, "error")
	if e == "ok" {
		return field(m, "online_ip") == ip, nil
	}
	if e == "not_online_error" || e == "ip_not_online_error" || e == "user_tab_error" {
		return false, nil
	}
	// Some deployments return ecode or res instead of error for offline clients.
	if e == "" && field(m, "res") == "not_online_error" {
		return false, nil
	}
	return false, fmt.Errorf("无法确认设备在线状态（error=%s，ecode=%s）", safe(e), safe(field(m, "ecode")))
}
func safe(s string) string {
	if len(s) > 80 {
		return "unknown"
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return "unknown"
		}
	}
	return s
}
func (c *Client) Login(ctx context.Context, user, password, ip string) error {
	m, err := c.get(ctx, "/cgi-bin/get_challenge", url.Values{"username": {user}, "ip": {ip}})
	if err != nil {
		return err
	}
	token := field(m, "challenge")
	if token == "" {
		return errors.New("未获取到 challenge，请检查学校配置")
	}
	mac := hmac.New(md5.New, []byte(token))
	mac.Write([]byte(password))
	digest := fmt.Sprintf("%x", mac.Sum(nil))
	raw, _ := json.Marshal(map[string]string{"username": user, "password": password, "ip": ip, "acid": c.school.ACID, "enc_ver": c.school.EncVer})
	info := "{SRBX1}" + base64.NewEncoding(alphabet).EncodeToString(xxteaEncrypt(raw, []byte(token)))
	checksum := fmt.Sprintf("%x", sha1.Sum([]byte(token+user+token+digest+token+c.school.ACID+token+ip+token+c.school.N+token+c.school.Type+token+info)))
	result, err := c.get(ctx, "/cgi-bin/srun_portal", url.Values{
		"action": {"login"}, "username": {user}, "password": {"{MD5}" + digest}, "ip": {ip}, "ac_id": {c.school.ACID},
		"n": {c.school.N}, "type": {c.school.Type}, "info": {info}, "chksum": {checksum}, "os": {"Windows 10"}, "name": {"Windows"}, "double_stack": {"0"},
	})
	if err != nil {
		return err
	}
	if field(result, "error") == "ok" {
		return nil
	}
	code := field(result, "ecode")
	if code == "E2620" || field(result, "error") == "ip_already_online_error" {
		online, err := c.Check(ctx, ip)
		if err == nil && online {
			return nil
		}
		return errors.New("服务器报告已在线，但设备状态尚未确认")
	}
	reasons := map[string]string{"E2531": "用户不存在", "E2901": "账号或密码错误", "E2606": "用户被禁用"}
	if reason, ok := reasons[code]; ok {
		return fmt.Errorf("%s（%s）", reason, code)
	}
	return fmt.Errorf("认证失败（error=%s，ecode=%s）", safe(field(result, "error")), safe(code))
}
