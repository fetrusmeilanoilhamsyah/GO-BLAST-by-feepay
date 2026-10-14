package wa

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Label salt/konteks khusus untuk penurunan kunci HMAC JID WhatsApp.
// Berbeda dari label payout_dest_hash (goblast_payout_dest_hash_salt).
const jidHashSalt = "goblast_wa_jid_hash_salt"

// DeriveJIDHashKey menurunkan sub-kunci dari ENCRYPTION_KEY khusus untuk konteks identitas JID WhatsApp.
func DeriveJIDHashKey(masterKey []byte) []byte {
	if len(masterKey) != 32 {
		return nil
	}
	h := hmac.New(sha256.New, masterKey)
	h.Write([]byte(jidHashSalt))
	return h.Sum(nil)
}

// HashWAJID menghitung HMAC-SHA256 deterministik dari bagian User JID WhatsApp (nomor saja tanpa domain @s.whatsapp.net).
func HashWAJID(user string, masterKey []byte) string {
	user = strings.TrimSpace(user)
	if user == "" || len(masterKey) != 32 {
		return ""
	}
	derived := DeriveJIDHashKey(masterKey)
	if derived == nil {
		return ""
	}
	mac := hmac.New(sha256.New, derived)
	mac.Write([]byte(user))
	return hex.EncodeToString(mac.Sum(nil))
}
