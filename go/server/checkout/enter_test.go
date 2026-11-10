package checkout

import (
	"regexp"
	"sort"
	"strconv"
	"testing"
)

// TestParseSpecValue 测试解析规格数值的函数
func TestParseSpecValue(t *testing.T) {
	// 模拟解析规格数值的函数
	parseSpecValue := func(spec string) int {
		re, err := regexp.Compile(`\d+`)
		if err != nil {
			return 0
		}
		numberStr := re.FindString(spec)
		number, err := strconv.Atoi(numberStr)
		if err != nil {
			return 0
		}
		return number
	}

	// 测试用例
	testCases := []struct {
		name     string
		spec     string
		expected int
	}{
		{"5斤", "5斤", 5},
		{"10斤", "10斤", 10},
		{"15斤", "15斤", 15},
		{"20斤", "20斤", 20},
		{"无数字", "无数字", 0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := parseSpecValue(tc.spec)
			if result != tc.expected {
				t.Errorf("parseSpecValue(%q) = %d, want %d", tc.spec, result, tc.expected)
			}
		})
	}
}

// TestSpecSorting 测试规格排序功能
func TestSpecSorting(t *testing.T) {
	// 模拟解析规格数值的函数
	parseSpecValue := func(spec string) int {
		re, err := regexp.Compile(`\d+`)
		if err != nil {
			return 0
		}
		numberStr := re.FindString(spec)
		number, err := strconv.Atoi(numberStr)
		if err != nil {
			return 0
		}
		return number
	}

	// 测试用例：未排序的规格列表
	unsortedSpecs := []string{"5斤", "15斤", "10斤", "20斤", "3斤"}

	// 对规格进行降序排序
	sort.Slice(unsortedSpecs, func(i, j int) bool {
		return parseSpecValue(unsortedSpecs[i]) > parseSpecValue(unsortedSpecs[j])
	})

	// 期望的排序结果
	expectedSpecs := []string{"20斤", "15斤", "10斤", "5斤", "3斤"}

	// 验证排序结果
	for i, spec := range unsortedSpecs {
		if spec != expectedSpecs[i] {
			t.Errorf("unsortedSpecs[%d] = %q, want %q", i, spec, expectedSpecs[i])
		}
	}
}
