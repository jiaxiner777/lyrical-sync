# LyricalSync

英文发音可视化训练 Web 应用的 Monorepo 脚手架。

## 目录结构

```text
lyrical-sync/
├── backend/            # Go 后端
├── frontend/           # Vue 3 前端
└── README.md           # 项目说明
```

## 后端

- Go Module: `lyrical-sync-backend`
- 框架: Gin
- 已配置 CORS，允许 `http://localhost:5173` 和 `http://127.0.0.1:5173`
- Mock API: `GET /api/song/mock`

启动后端：

```bash
cd backend
go run .
```

默认监听：

```text
http://localhost:8080
```

接口地址：

```text
http://localhost:8080/api/song/mock
```

## 前端

- Vite
- Vue 3
- TypeScript
- Tailwind CSS

启动前端：

```bash
cd frontend
npm install
npm run dev
```

默认访问：

```text
http://localhost:5173
```

前端页面会在启动后请求：

```text
http://localhost:8080/api/song/mock
```

## 构建检查

后端构建：

```bash
cd backend
go build .
```

前端构建：

```bash
cd frontend
npm run build
```
