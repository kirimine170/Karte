# windows環境でのセットアップ

## 検証環境

```txt
# System
┌──────────────────────────────────────────────────────────────────────────────┐
| OS           | Windows 10 Home                                               |
| Version      | 2009 (Build: 26100)                                           |
| ID           | 24H2                                                          |
| Branding     | Windows 11 Home                                               |
| Go Version   | go1.24.4                                                      |
| Platform     | windows                                                       |
| Architecture | amd64                                                         |
| CPU          | Intel(R) Core(TM) i5-10600 CPU @ 3.30GHz                      |
| GPU          | NVIDIA GeForce RTX 2070 SUPER (NVIDIA) - Driver: 32.0.15.8129 |
| Memory       | 32GB                                                          |
└──────────────────────────────────────────────────────────────────────────────┘

# Dependencies
┌───────────────────────────────────────────────────────┐
| Dependency | Package Name | Status    | Version       |
| WebView2   | N/A          | Installed | 142.0.3595.94 |
| Nodejs     | N/A          | Installed | 24.11.1       |
| npm        | N/A          | Installed | 11.6.2        |
| *upx       | N/A          | Available |               |
| *nsis      | N/A          | Available |               |
|                                                       |
└─────────────── * - Optional Dependency ───────────────┘
```

## npm周り

必要なパッケージをインストールする

```sh
Karte> cd frontend
Karte\frontend> npm install
Karte\frontend> npm run build
```

## wailsを動かす

```sh
Karte> wails build
# もしくは
Karte> wails dev
```
