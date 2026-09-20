# komari-slim

> **【非官方精简版 / Unofficial trimmed fork】**
>
> 本项目是 [komari-monitor](https://github.com/komari-monitor) 的非官方精简修改版，**仅供个人学习与自用，禁止商业用途**。
> 若原作者或任何权利人对本衍生版本有异议，请提 Issue，**我会立即删除本仓库及相关发布物**。
>
> This is an unofficial, trimmed derivative of komari-monitor, for **personal study and
> non-commercial use only**. If any rights holder objects, please open an issue and
> **I will remove this repository and its releases immediately**.
>
> 详见 [DISCLAIMER.md](./DISCLAIMER.md)

<div align="center">
  <img src="docs/logo.png" width="460" alt="komari-slim"/>
</div>

![Badge](https://hitscounter.dev/api/hit?url=https%3A%2F%2Fgithub.com%2Faomtest%2Fkomari-slim-server&label=&icon=github&color=%23a370f7&message=&style=flat&tz=UTC)

[English](./README.md) | [简体中文](./README_zh-cn.md)

Komari 是一款轻量级的自托管服务器监控工具，旨在提供简单、高效的服务器性能监控解决方案。它支持通过 Web 界面查看服务器状态，并通过轻量级 Agent 收集数据。

> [!WARNING]
> Komari 是一款自托管的服务器监控程序，仅应部署在你拥有或已获得授权管理的系统上。用户需自行承担部署和使用 Komari 的责任，开发者不对由此产生的后果承担责任。

[文档](https://www.komari.wiki/)

## 特性

- **实时监控**: 秒级实时数据展示。
- **轻量高效**：低资源占用，适合各种规模的服务器。
- **自托管**：完全掌控数据隐私，部署简单。
- **Web 界面**：直观的监控仪表盘，易于使用。
- **极强的可扩展性**: 支持自定义主题和插件。

## 快速开始

### 一键安装（推荐）

```bash
curl -fsSL https://raw.githubusercontent.com/aomtest/komari-slim-server/main/install-komari-slim.sh | bash
```

脚本运行后按提示选择语言与版本即可，默认安装到 `/opt/komari` 并注册 systemd 服务。

> 国内访问 `raw.githubusercontent.com` 可能失败，可先手动下载脚本再执行：
> ```bash
> curl -fsSL -o install-komari-slim.sh https://raw.githubusercontent.com/aomtest/komari-slim-server/main/install-komari-slim.sh
> bash install-komari-slim.sh
> ```

### 手动安装

预编译二进制见 [Releases](https://github.com/aomtest/komari-slim-server/releases)（Linux amd64 / arm64、Windows amd64）。

Docker、源码构建和更新说明，请参阅 [安装指南](https://www.komari.wiki/install/quick-start).

## 截图

| 页面         | 截图                                                                                                                                                         |
| ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 主页仪表盘   | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E4%B8%BB%E9%A1%B5%E4%BB%AA%E8%A1%A8%E7%9B%98.webp" width="800" alt="主页仪表盘">            |
| 后台仪表盘   | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E5%90%8E%E5%8F%B0%E4%BB%AA%E8%A1%A8%E7%9B%98.webp" width="800" alt="后台仪表盘">            |
| 历史图表     | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E5%8E%86%E5%8F%B2%E5%9B%BE%E8%A1%A8.webp" width="800" alt="历史图表">                       |
| 主题可自定义 | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E4%B8%BB%E9%A2%98%E5%8F%AF%E8%87%AA%E5%AE%9A%E4%B9%89.webp" width="800" alt="主题可自定义"> |
| 主题市场     | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E4%B8%BB%E9%A2%98%E5%B8%82%E5%9C%BA.webp" width="800" alt="主题市场">                       |
## 贡献者

感谢所有为 Komari 贡献代码、主题、插件、文档、翻译、问题报告或反馈的朋友。

<a href="https://github.com/komari-monitor/komari/graphs/contributors"><img src="https://contributors-img.web.app/image?repo=komari-monitor/komari" alt="Komari 贡献者" width="600"></a>

---

本项目为个人自用的非官方衍生版本，**不接受任何形式的捐赠或赞助**。
