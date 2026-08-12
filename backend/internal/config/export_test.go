package config

import "github.com/ramoncjs3/fries/internal/perm"

// 把注册表的两个内部判据透给外部测试。
//
// 放在 `export_test.go` 里，所以**只存在于测试构建中** —— 生产代码里没有这两个函数，
// 业务代码想绕过白名单也没得绕：唯一的入口仍然是 Set / SetPlatform。

// CheckRegistryForTest 跑注册表自检。
func CheckRegistryForTest() error { return CheckRegistry() }

// WritableForTest 判断某个 key 能不能通过对应 Realm 的写入口写进去。
func WritableForTest(key string, realm perm.Realm) bool { return writable(key, realm) }
