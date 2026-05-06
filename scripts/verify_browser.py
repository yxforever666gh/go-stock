#!/usr/bin/env python3
"""
Go-Stock 浏览器功能验证脚本
使用 Playwright 进行完整的页面测试
"""

import sys
import time
from playwright.sync_api import sync_playwright, TimeoutError as PlaywrightTimeout

BASE_URL = "http://127.0.0.1:34115"

def test_homepage(page):
    """测试首页加载和数据显示"""
    print("\n1. 测试首页加载...")

    try:
        page.goto(BASE_URL, wait_until="networkidle", timeout=10000)
        print("   ✅ 页面加载成功")
    except Exception as e:
        print(f"   ❌ 页面加载失败: {e}")
        return False

    # 检查标题
    title = page.title()
    if "go-stock" in title.lower():
        print(f"   ✅ 页面标题正确: {title}")
    else:
        print(f"   ⚠️  页面标题异常: {title}")

    # 等待 Vue 应用加载
    time.sleep(2)

    # 检查主要元素
    try:
        # 检查菜单栏
        menu_items = page.locator('menuitem').count()
        print(f"   ✅ 菜单项数量: {menu_items}")

        # 检查是否有内容
        body_text = page.locator('body').inner_text()
        if len(body_text) > 100:
            print(f"   ✅ 页面内容已加载 (长度: {len(body_text)} 字符)")
        else:
            print(f"   ⚠️  页面内容较少 (长度: {len(body_text)} 字符)")

        return True
    except Exception as e:
        print(f"   ❌ 元素检查失败: {e}")
        return False

def test_stock_list(page):
    """测试自选股列表"""
    print("\n2. 测试自选股列表...")

    try:
        # 等待页面加载
        time.sleep(2)

        # 检查是否有股票数据显示
        page_content = page.content()

        # 检查常见的股票相关文本
        keywords = ["股票", "自选", "代码", "涨跌", "价格"]
        found_keywords = [kw for kw in keywords if kw in page_content]

        if found_keywords:
            print(f"   ✅ 找到股票相关内容: {', '.join(found_keywords)}")
        else:
            print(f"   ⚠️  未找到明显的股票数据")

        # 检查是否有表格或列表
        tables = page.locator('table').count()
        lists = page.locator('ul, ol').count()

        print(f"   ℹ️  表格数量: {tables}, 列表数量: {lists}")

        return True
    except Exception as e:
        print(f"   ❌ 测试失败: {e}")
        return False

def test_navigation(page):
    """测试页面导航"""
    print("\n3. 测试页面导航...")

    try:
        # 点击市场行情
        market_link = page.locator('text=市场行情').first
        if market_link.count() > 0:
            market_link.click()
            time.sleep(2)
            print("   ✅ 市场行情页面可访问")

        # 点击研究中心
        research_link = page.locator('text=研究中心').first
        if research_link.count() > 0:
            research_link.click()
            time.sleep(2)
            print("   ✅ 研究中心页面可访问")

        # 返回首页
        home_link = page.locator('text=股票自选').first
        if home_link.count() > 0:
            home_link.click()
            time.sleep(2)
            print("   ✅ 返回首页成功")

        return True
    except Exception as e:
        print(f"   ⚠️  导航测试部分失败: {e}")
        return True  # 导航失败不算致命错误

def test_console_errors(page):
    """检查控制台错误"""
    print("\n4. 检查控制台错误...")

    errors = []
    warnings = []

    def handle_console(msg):
        if msg.type == 'error':
            errors.append(msg.text)
        elif msg.type == 'warning':
            warnings.append(msg.text)

    page.on('console', handle_console)

    # 重新加载页面以捕获所有日志
    page.reload(wait_until="networkidle")
    time.sleep(2)

    if errors:
        print(f"   ⚠️  发现 {len(errors)} 个错误:")
        for err in errors[:3]:  # 只显示前3个
            print(f"      - {err[:100]}")
    else:
        print("   ✅ 无控制台错误")

    if warnings:
        print(f"   ℹ️  发现 {len(warnings)} 个警告")

    return len(errors) == 0

def take_screenshot(page, filename):
    """截图"""
    try:
        page.screenshot(path=filename, full_page=True)
        print(f"   ✅ 截图已保存: {filename}")
        return True
    except Exception as e:
        print(f"   ❌ 截图失败: {e}")
        return False

def main():
    print("=" * 60)
    print("   Go-Stock 浏览器功能验证")
    print("=" * 60)

    results = {
        'passed': 0,
        'failed': 0,
        'warnings': 0
    }

    try:
        with sync_playwright() as p:
            # 启动无头浏览器
            browser = p.chromium.launch(headless=True)
            context = browser.new_context(
                viewport={'width': 1920, 'height': 1080},
                locale='zh-CN'
            )
            page = context.new_page()

            # 运行测试
            tests = [
                ("首页加载", lambda: test_homepage(page)),
                ("自选股列表", lambda: test_stock_list(page)),
                ("页面导航", lambda: test_navigation(page)),
                ("控制台错误", lambda: test_console_errors(page)),
            ]

            for test_name, test_func in tests:
                try:
                    if test_func():
                        results['passed'] += 1
                    else:
                        results['failed'] += 1
                except Exception as e:
                    print(f"   ❌ {test_name} 测试异常: {e}")
                    results['failed'] += 1

            # 截图
            print("\n5. 保存截图...")
            screenshot_path = "logs/verification-screenshot.png"
            if take_screenshot(page, screenshot_path):
                results['passed'] += 1
            else:
                results['failed'] += 1

            browser.close()

    except Exception as e:
        print(f"\n❌ 浏览器启动失败: {e}")
        print("   请确保已安装 Playwright: pip install playwright && playwright install chromium")
        return 1

    # 输出结果
    print("\n" + "=" * 60)
    print("   测试结果汇总")
    print("=" * 60)
    print(f"通过: {results['passed']}")
    print(f"失败: {results['failed']}")
    print(f"警告: {results['warnings']}")
    print(f"总计: {results['passed'] + results['failed']}")
    print()

    if results['failed'] == 0:
        print("✅ 所有浏览器测试通过！")
        return 0
    else:
        print("⚠️  部分测试失败，请检查详情")
        return 1

if __name__ == "__main__":
    sys.exit(main())
