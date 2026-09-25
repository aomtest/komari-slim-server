package accounts

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/aomtest/komari-slim-server/database/dbcore"
	"github.com/aomtest/komari-slim-server/database/models"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// constantSalt
//
// Deprecated: 仅用于验证存量旧哈希（SHA256(password + constantSalt)）。
// 新密码一律使用 bcrypt 哈希；constantSalt 不得再用于任何新哈希的生成。
const constantSalt = "06Wm4Jv1Hkxx"

// CheckPassword 检查密码是否正确
//
// 如果密码正确，返回用户的 UUID 和 true；否则返回空字符串和 false。
// 兼容 bcrypt 与旧版哈希两种格式：当旧版哈希验证通过时，会自动升级为
// bcrypt 并写回数据库，整个过程对用户透明，无需任何手动操作。
func CheckPassword(username, passwd string) (uuid string, success bool) {
	db := dbcore.GetDBInstance()
	var user models.User
	result := db.Where("username = ?", username).First(&user)
	if result.Error != nil {
		// 静默处理错误，不显示日志
		return "", false
	}
	ok, upgradedHash := verifyPasswd(passwd, user.Passwd)
	if !ok {
		return "", false
	}
	if upgradedHash != "" {
		// 旧哈希验证通过：透明升级为 bcrypt。写回失败不影响本次登录结果。
		db.Model(&models.User{}).Where("uuid = ?", user.UUID).Update("passwd", upgradedHash)
	}
	return user.UUID, true
}

// ForceResetPassword 强制重置用户密码
func ForceResetPassword(username, passwd string) (err error) {
	hashedPassword, err := hashPasswd(passwd)
	if err != nil {
		return err
	}
	db := dbcore.GetDBInstance()
	result := db.Model(&models.User{}).Where("username = ?", username).Update("passwd", hashedPassword)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("无法找到用户名")
	}
	return nil
}

// hashPasswd 对密码进行 bcrypt 哈希。
//
// 这是新密码的唯一写入路径：所有新建、修改、重置密码的流程都必须经过它。
func hashPasswd(passwd string) (string, error) {
	if len(passwd) > 72 {
		return "", fmt.Errorf("密码过长：bcrypt 最多支持 72 字节")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(passwd), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// legacyHashPasswd 计算旧版 SHA256(password + constantSalt) 哈希。
//
// 仅用于验证存量旧哈希；禁止用于任何新密码的生成。
func legacyHashPasswd(passwd string) string {
	saltedPassword := passwd + constantSalt
	hash := sha256.New()
	hash.Write([]byte(saltedPassword))
	hashedPassword := base64.StdEncoding.EncodeToString(hash.Sum(nil))
	return hashedPassword
}

// verifyPasswd 验证密码，兼容 bcrypt 与旧版哈希。
//
// 返回 (是否通过, 升级后的 bcrypt 哈希)：当旧版哈希验证通过时，第二个
// 返回值是新生成的 bcrypt 哈希，调用方应将其写回数据库以完成透明升级；
// bcrypt 验证通过或验证失败时，第二个返回值为空字符串。
func verifyPasswd(passwd, storedHash string) (ok bool, upgradedHash string) {
	if strings.HasPrefix(storedHash, "$2") {
		if bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(passwd)) == nil {
			return true, ""
		}
		return false, ""
	}
	// 旧版哈希路径
	if legacyHashPasswd(passwd) != storedHash {
		return false, ""
	}
	newHash, err := hashPasswd(passwd)
	if err != nil {
		// 极端情况（密码过长）：本次登录仍视为通过，但无法完成升级
		return true, ""
	}
	return true, newHash
}

func CreateAccount(username, passwd string) (user models.User, err error) {
	return CreateAccountWithDB(dbcore.GetDBInstance(), username, passwd)
}

func CreateAccountWithDB(db *gorm.DB, username, passwd string) (user models.User, err error) {
	hashedPassword, err := hashPasswd(passwd)
	if err != nil {
		return models.User{}, err
	}
	user = models.User{
		UUID:     uuid.New().String(),
		Username: username,
		Passwd:   hashedPassword,
	}
	err = db.Create(&user).Error
	if err != nil {
		return models.User{}, err
	}
	return user, nil
}

func DeleteAccountByUsername(username string) (err error) {
	return DeleteAccountByUsernameWithDB(dbcore.GetDBInstance(), username)
}

func DeleteAccountByUsernameWithDB(db *gorm.DB, username string) (err error) {
	err = db.Where("username = ?", username).Delete(&models.User{}).Error
	if err != nil {
		return err
	}
	return nil
}

func GetUserByUUID(uuid string) (user models.User, err error) {
	db := dbcore.GetDBInstance()
	err = db.Where("uuid = ?", uuid).First(&user).Error
	if err != nil {
		return models.User{}, err
	}
	return user, nil
}

// 通过 SSO 信息获取用户
func GetUserBySSO(ssoID string) (user models.User, err error) {
	db := dbcore.GetDBInstance()

	// 首先尝试查找已存在的用户
	err = db.Where("sso_id = ?", ssoID).First(&user).Error
	if err == nil {
		return user, nil
	}

	// 如果找不到用户，返回明确的错误信息
	return models.User{}, fmt.Errorf("用户不存在：%s", ssoID)
}

func BindingExternalAccount(uuid string, sso_id string) error {
	db := dbcore.GetDBInstance()
	err := db.Model(&models.User{}).Where("uuid = ?", uuid).Update("sso_id", sso_id).Error
	if err != nil {
		return err
	}
	return nil
}

func UnbindExternalAccount(uuid string) error {
	db := dbcore.GetDBInstance()
	err := db.Model(&models.User{}).Where("uuid = ?", uuid).Update("sso_id", "").Error
	if err != nil {
		return err
	}
	return nil
}

func UpdateUser(uuid string, name, password, sso_type *string) error {
	db := dbcore.GetDBInstance()
	// Check if user exists
	var existingUser models.User
	result := db.Where("uuid = ?", uuid).First(&existingUser)
	if result.Error != nil {
		return fmt.Errorf("user not found: %s", uuid)
	}
	updates := make(map[string]interface{})
	if name != nil {
		updates["username"] = *name
	}
	if password != nil {
		hashedPassword, err := hashPasswd(*password)
		if err != nil {
			return err
		}
		updates["passwd"] = hashedPassword
	}
	if sso_type != nil {
		updates["sso_type"] = *sso_type
	}
	updates["updated_at"] = time.Now().UTC()
	err := db.Model(&models.User{}).Where("uuid = ?", uuid).Updates(updates).Error
	if err != nil {
		return err
	}
	if password != nil {
		DeleteAllSessions()
	}
	return nil
}
