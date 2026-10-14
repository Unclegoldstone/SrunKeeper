package srun

import "encoding/binary"

// xxteaEncrypt 对 data 使用 key 进行 XXTEA 加密，返回加密后的字节序列。
func xxteaEncrypt(data, key []byte) []byte {
	if len(data) == 0 {
		return nil
	}

	// ---- 将 data 转为 uint32 切片（小端序），末尾追加原始长度 ----
	pad := (4 - len(data)%4) % 4
	padded := make([]byte, len(data)+pad)
	copy(padded, data)

	n := len(padded) / 4
	v := make([]uint32, n+1)
	for i := 0; i < n; i++ {
		v[i] = binary.LittleEndian.Uint32(padded[i*4:])
	}
	v[n] = uint32(len(data)) // 原始长度

	// ---- 将 key 转为 uint32 切片（至少 4 个元素） ----
	kp := make([]byte, len(key))
	copy(kp, key)
	if r := len(kp) % 4; r != 0 {
		kp = append(kp, make([]byte, 4-r)...)
	}
	k := make([]uint32, len(kp)/4)
	for i := range k {
		k[i] = binary.LittleEndian.Uint32(kp[i*4:])
	}
	if len(k) < 4 {
		tmp := make([]uint32, 4)
		copy(tmp, k)
		k = tmp
	}

	// ---- XXTEA 加密核心 ----
	n = len(v) - 1
	if n < 1 {
		return nil
	}
	z := v[n]
	const delta uint32 = 0x9E3779B9
	var d uint32
	const roundsBase = 6

	q := roundsBase + 52/(n+1)
	for q > 0 {
		d += delta
		e := (d >> 2) & 3
		p := 0
		for p < n {
			y := v[p+1]
			m := (z>>5 ^ y<<2) + ((y>>3 ^ z<<4) ^ (d ^ y)) + (k[(p&3)^int(e)] ^ z)
			v[p] += m
			z = v[p]
			p++
		}
		y := v[0]
		m := (z>>5 ^ y<<2) + ((y>>3 ^ z<<4) ^ (d ^ y)) + (k[(n&3)^int(e)] ^ z)
		v[n] += m
		z = v[n]
		q--
	}

	// ---- 转回 []byte ----
	out := make([]byte, len(v)*4)
	for i, x := range v {
		binary.LittleEndian.PutUint32(out[i*4:], x)
	}
	return out
}
