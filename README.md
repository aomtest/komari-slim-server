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
  <img src="docs/logo.png" width="100%" alt="komari-slim"/>
</div>

![Badge](https://hitscounter.dev/api/hit?url=https%3A%2F%2Fgithub.com%2Faomtest%2Fkomari-slim-server&label=&icon=github&color=%23a370f7&message=&style=flat&tz=UTC)

[English](./README.md) | [简体中文](./README_zh-cn.md)

komari-slim is a lightweight, self-hosted server monitoring tool — a trimmed fork of Komari focused purely on performance monitoring. It tracks server metrics through a web dashboard, with data collected by a lightweight agent, while stripping out execution surfaces (terminal, file manager, command dispatch, plugins) to keep the attack surface minimal.

> [!WARNING]
> Komari is a self-hosted server monitoring application. Deploy it only on systems you own or are authorized to manage. You are solely responsible for how you deploy and use it. The developers accept no liability for any consequences arising from its deployment or use.

[Documentation](https://www.komari.wiki/)

## Features

- **Real-time monitoring**: Displays monitoring data at one-second intervals.
- **Lightweight and efficient**: Uses minimal system resources and works well on servers of any size.
- **Self-hosted**: Keeps you in control of your data and privacy.
- **Web interface**: Provides an intuitive, easy-to-use monitoring dashboard.
- **Extensible**: Supports custom themes and plugins.

## Quick Start

### One-click install (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/aomtest/komari-slim-server/main/install-komari-slim.sh | bash
```

Follow the prompts to pick your language and edition. It installs to `/opt/komari` and registers a systemd service by default.

> If `raw.githubusercontent.com` is unreachable, download the script first and run it locally:
> ```bash
> curl -fsSL -o install-komari-slim.sh https://raw.githubusercontent.com/aomtest/komari-slim-server/main/install-komari-slim.sh
> bash install-komari-slim.sh
> ```

### Manual install

Prebuilt binaries are on the [Releases](https://github.com/aomtest/komari-slim-server/releases) page (Linux amd64 / arm64, Windows amd64).

For instructions on Docker deployment, binary installation, building from source, and updates, see the [installation guide](https://www.komari.wiki/en/install/quick-start).

## Screenshots

| Page                | Screenshot                                                                                                                                                             |
| ------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Home Dashboard      | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E4%B8%BB%E9%A1%B5%E4%BB%AA%E8%A1%A8%E7%9B%98-en.webp" width="800" alt="Home Dashboard">               |
| Admin Dashboard     | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E5%90%8E%E5%8F%B0%E4%BB%AA%E8%A1%A8%E7%9B%98-en.webp" width="800" alt="Admin Dashboard">              |
| History Charts      | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E5%8E%86%E5%8F%B2%E5%9B%BE%E8%A1%A8-en.webp" width="800" alt="History Charts">                        |
| Customizable Themes | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E4%B8%BB%E9%A2%98%E5%8F%AF%E8%87%AA%E5%AE%9A%E4%B9%89-en.webp" width="800" alt="Customizable Themes"> |
| Theme Market        | <img src="https://b2.akz.moe/awesome-pictures/komari-screenshot/%E4%B8%BB%E9%A2%98%E5%B8%82%E5%9C%BA-en.webp" width="800" alt="Theme Market">                          |
## Contributors

Thanks to everyone who has contributed code, themes, plugins, documentation, translations, bug reports, or feedback to Komari.

<a href="https://github.com/komari-monitor/komari/graphs/contributors"><img src="https://contributors-img.web.app/image?repo=komari-monitor/komari" alt="Komari contributors" width="600"></a>

---

This is an unofficial derivative maintained for personal use.
**No donations or sponsorships are accepted.**
