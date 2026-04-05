package util

import (
	"fmt"
	"testing"
)

func TestMd(t *testing.T) {
	// 示例使用：单个结构体
	user := User{
		Name:  "张三",
		Age:   30,
		Email: "zhangsan@example.com",
		Address: Address{
			City:    "北京",
			Country: "中国",
		},
		Phones: []string{"13800138000", "13900139000"},
		Active: true,
	}

	fmt.Println("单个结构体转换:")
	fmt.Println(MarkdownTable(user))
	fmt.Println()

	// 示例使用：结构体切片
	users := []User{
		{
			Name:  "张三",
			Age:   30,
			Email: "zhangsan@example.com",
			Address: Address{
				City:    "北京",
				Country: "中国",
			},
			Phones: []string{"13800138000"},
			Active: true,
		},
		{
			Name:  "李四",
			Age:   25,
			Email: "lisi@example.com",
			Address: Address{
				City:    "上海",
				Country: "中国",
			},
			Phones: []string{"13900139000", "13700137000"},
			Active: false,
		},
	}

	fmt.Println("结构体切片转换:")
	fmt.Println(MarkdownTable(users))
}
