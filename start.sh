#!/bin/bash

# OmniBot 启动脚本
# 支持命令：
#   dev      - 开发模式：同时启动后端和前端开发服务器
#   build    - 完整构建：打包前端 + 编译后端二进制（包含嵌入的前端资源）
#   start    - 启动已编译的二进制文件
#   all      - 完整流程：构建后直接启动
#   clean    - 清理构建产物

set -e

# 配置
CONFIG_PATH="configs/config.yaml"
BIN_PATH="bin/omnibot"

show_help() {
    echo "OmniBot 启动脚本"
    echo "用法: $0 [命令]"
    echo ""
    echo "命令:"
    echo "  dev      开发模式：同时启动后端和前端开发服务器"
    echo "  build    完整构建：打包前端 + 编译后端二进制"
    echo "  start    启动已编译的二进制文件"
    echo "  all      完整流程：构建后直接启动"
    echo "  clean    清理构建产物"
    echo "  help     显示帮助信息"
}

cmd_dev() {
    echo "🚀 启动开发模式..."
    echo "前端地址: http://127.0.0.1:5173/"
    echo "后端地址: http://127.0.0.1:8080/"
    echo "按 Ctrl+C 停止所有服务"

    # 捕获Ctrl+C信号，停止所有子进程
    trap 'echo "🛑 正在停止服务..."; kill 0' SIGINT

    # 启动前端
    (cd frontend && npm run dev) &
    FRONTEND_PID=$!

    # 启动后端
    go run cmd/server/main.go -config "$CONFIG_PATH" &
    BACKEND_PID=$!

    # 等待所有进程结束
    wait
}

cmd_build() {
    echo "🔨 开始完整构建..."

    # 构建前端
    echo "📦 打包前端资源..."
    cd frontend
    npm install
    npm run build
    cd ..

    # 构建后端
    echo "🔧 编译后端二进制..."
    mkdir -p bin
    go build -o "$BIN_PATH" cmd/server/main.go

    echo "✅ 构建完成！二进制文件路径: $BIN_PATH"
}

cmd_start() {
    if [ ! -f "$BIN_PATH" ]; then
        echo "❌ 二进制文件不存在，请先执行 $0 build"
        exit 1
    fi

    echo "🚀 启动 OmniBot 服务..."
    echo "访问地址: http://127.0.0.1:8080/"
    "$BIN_PATH" -config "$CONFIG_PATH"
}

cmd_all() {
    cmd_build
    cmd_start
}

cmd_clean() {
    echo "🧹 清理构建产物..."
    rm -rf bin
    rm -rf frontend/dist
    echo "✅ 清理完成"
}

# 主逻辑
case "$1" in
    dev)
        cmd_dev
        ;;
    build)
        cmd_build
        ;;
    start)
        cmd_start
        ;;
    all)
        cmd_all
        ;;
    clean)
        cmd_clean
        ;;
    help|--help|-h)
        show_help
        ;;
    *)
        echo "❌ 未知命令: $1"
        show_help
        exit 1
        ;;
esac
