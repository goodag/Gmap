# Google Map 商家搜索系统

一个基于 Go 语言开发的 Google Map 商家搜索与数据采集系统，支持地图搜索、网页爬取、AI分析和邮件发送等功能。

## 功能特性

### 🗺️ 地图搜索
- 基于 Google Maps API 的商家搜索
- 支持按关键词、地理位置、半径搜索
- 自动分页获取搜索结果
- 支持全球多个城市定位

### 🔍 谷歌搜索
- 基于 Google Custom Search 的网页搜索
- 批量搜索并爬取目标网站
- 支持多页搜索结果

### 📧 邮箱爬取
- 使用 Rod 浏览器自动化爬取
- 支持单个或批量网站爬取
- 智能提取邮箱地址和联系方式

### 🤖 AI 分析
- 集成豆包 AI API
- 自动分析公司信息
- 智能评分匹配度

### ✉️ 邮件发送
- 支持单个和批量邮件发送
- 邮件发送日志记录
- 发送冷却控制

### ⏰ 定时任务
- 支持 Cron 表达式
- 地图搜索和谷歌搜索定时任务
- 任务执行日志记录

### 📊 数据导出
- Excel 格式导出
- 支持按搜索记录导出
- 支持全部数据导出

## 技术栈

- **语言**: Go 1.24+
- **框架**: Gin 1.9
- **数据库**: MySQL 5.7+ / MariaDB
- **浏览器自动化**: Rod (Go)
- **爬虫框架**: Colly
- **ORM**: GORM
- **Excel处理**: Excelize
- **邮件发送**: gomail

## 项目结构

```
googleMap/
├── config/           # 配置文件
│   ├── config.go     # 配置加载
│   └── cities.json   # 城市数据
├── handlers/         # HTTP处理器
│   ├── search.go     # 地图搜索接口
│   ├── scraper.go    # 爬取接口
│   ├── google_search.go # 谷歌搜索接口
│   ├── email.go      # 邮件发送接口
│   ├── cron.go       # 定时任务接口
│   └── export.go     # 数据导出接口
├── services/         # 业务服务
│   ├── google.go     # Google API服务
│   ├── google_search.go # 谷歌搜索服务
│   ├── scraper.go    # 爬取服务
│   ├── rod_maps.go   # Rod浏览器服务
│   ├── email.go      # 邮件服务
│   ├── filter.go     # 数据过滤服务
│   ├── doubao.go     # 豆包AI服务
│   └── scheduler.go  # 定时任务调度
├── models/           # 数据库模型
│   └── models.go     # 实体定义
├── templates/        # HTML模板
│   ├── index.html    # 首页
│   ├── admin.html    # 后台管理
│   ├── google_search.html # 谷歌搜索页面
│   └── cron.html     # 定时任务页面
├── config.json       # 应用配置
├── main.go           # 入口文件
└── go.mod            # 依赖管理
```

## 快速开始

### 环境要求

- Go 1.24+
- MySQL 5.7+ 或 MariaDB
- Google Maps API Key
- Google Custom Search Engine ID（可选）

### 安装依赖

```bash
go mod download
```

### 配置文件

复制并修改 `config.json`：

```json
{
  "server": {
    "port": "8088"
  },
  "database": {
    "host": "localhost",
    "port": "3306",
    "user": "root",
    "password": "your_password",
    "dbname": "google_search_company"
  },
  "google": {
    "api_key": "YOUR_GOOGLE_MAPS_API_KEY",
    "custom_search_id": "YOUR_CUSTOM_SEARCH_ENGINE_ID"
  },
  "email": {
    "smtp_host": "smtp.gmail.com",
    "smtp_port": 587,
    "username": "your-email@gmail.com",
    "password": "your-app-password",
    "from_name": "商家搜索系统"
  },
  "proxy": {
    "enabled": false,
    "address": "",
    "chrome_path": ""
  },
  "doubao": {
    "api_key": "YOUR_DOUBAO_API_KEY",
    "model_id": "Doubao-Seed-2.0-lite",
    "base_url": "https://ark.cn-beijing.volces.com/api/v3",
    "enabled": false
  }
}
```

### 运行项目

```bash
go run main.go
```

启动后访问：
- 首页: http://localhost:8088
- 后台管理: http://localhost:8088/admin
- 谷歌搜索: http://localhost:8088/google-search
- 定时任务: http://localhost:8088/cron

## API 接口

### 地图搜索

| 接口 | 方法 | 说明 |
|------|------|------|
| `/api/search` | POST | 搜索商家 |
| `/api/search/next` | POST | 获取下一页 |
| `/api/place/detail` | GET | 获取地点详情 |
| `/api/companies` | GET | 获取公司列表 |

### 谷歌搜索

| 接口 | 方法 | 说明 |
|------|------|------|
| `/api/google-search` | POST | 执行谷歌搜索 |
| `/api/google-search/tasks` | GET | 获取搜索任务 |
| `/api/google-search/results` | GET | 获取搜索结果 |

### 爬取

| 接口 | 方法 | 说明 |
|------|------|------|
| `/api/scrape` | POST | 爬取单个网站 |
| `/api/scrape/batch` | POST | 批量爬取 |

### 邮件

| 接口 | 方法 | 说明 |
|------|------|------|
| `/api/email/send` | POST | 发送邮件 |
| `/api/email/batch-send` | POST | 批量发送 |
| `/api/email/logs` | GET | 获取发送日志 |

### 定时任务

| 接口 | 方法 | 说明 |
|------|------|------|
| `/api/cron` | POST | 创建任务 |
| `/api/cron` | PUT | 更新任务 |
| `/api/cron/:id` | DELETE | 删除任务 |
| `/api/cron/:id/toggle` | PUT | 启用/禁用 |
| `/api/cron/:id/run` | POST | 立即执行 |

### 数据导出

| 接口 | 方法 | 说明 |
|------|------|------|
| `/api/export/record/:id` | GET | 按记录导出 |
| `/api/export/all` | GET | 导出全部数据 |

## 数据库表结构

### search_records - 搜索记录
- `id` - 主键
- `source` - 来源 (map/google)
- `keyword` - 搜索关键词
- `latitude/longitude` - 坐标
- `radius` - 搜索半径
- `status` - 状态 (0进行中/1完成/2失败)

### companies - 公司信息
- `id` - 主键
- `place_id` - Google Place ID
- `name` - 公司名称
- `formatted_address` - 地址
- `phone` - 电话
- `email` - 邮箱
- `website` - 网站
- `domain` - 域名
- `rating` - 评分
- `types` - 类型

### company_emails - 公司邮箱
- `id` - 主键
- `company_id` - 公司ID
- `email` - 邮箱地址
- `source` - 来源

### email_logs - 邮件日志
- `id` - 主键
- `company_id` - 公司ID
- `to_email` - 收件人
- `subject` - 主题
- `status` - 状态

### cron_tasks - 定时任务
- `id` - 主键
- `name` - 任务名称
- `task_type` - 任务类型
- `cron_expr` - Cron表达式
- `enabled` - 是否启用

## 使用注意事项

1. **API Key 配置**: 请确保正确配置 Google Maps API Key 和其他第三方服务密钥
2. **代理设置**: 如果需要访问 Google 服务，可能需要配置代理
3. **浏览器依赖**: Rod 浏览器自动化需要安装 Chromium
4. **邮件发送**: Gmail 需要使用 App Password

## 许可证

MIT License

## 贡献

欢迎提交 Issue 和 Pull Request！
