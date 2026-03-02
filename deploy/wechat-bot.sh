#!/bin/bash

# 项目根目录
PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"

# 可执行文件路径
EXECUTABLE="$PROJECT_DIR/wechat-intelligent-bot"

# PID文件路径
PID_FILE="$PROJECT_DIR/wechat-bot.pid"

# 日志文件路径
LOG_FILE="$PROJECT_DIR/wechat-bot.log"

# 编译项目
build() {
    echo "正在编译项目..."
    cd "$PROJECT_DIR"
    export GOPROXY=https://goproxy.cn,direct
    export PATH=$PATH:/usr/local/go/bin
    /usr/local/go/bin/go mod tidy
    /usr/local/go/bin/go build -o "$EXECUTABLE" cmd/server/main.go
    if [ $? -eq 0 ]; then
        echo "编译成功！"
    else
        echo "编译失败！"
        exit 1
    fi
}

# 启动服务
start() {
    # 解析命令行参数
    local env=""
    while [ "$2" != "" ]; do
        case "$2" in
            -env=*)
                env="${2#*=}"
                ;;
            *)
                echo "未知参数: $2"
                help
                return
                ;;
        esac
        shift
    done
    
    if [ -f "$PID_FILE" ]; then
        PID=$(cat "$PID_FILE")
        if ps -p "$PID" > /dev/null 2>&1; then
            echo "服务已经在运行中，PID: $PID"
            return
        else
            echo "发现PID文件，但服务未运行，删除PID文件"
            rm "$PID_FILE"
        fi
    fi

    echo "正在启动服务..."
    cd "$PROJECT_DIR"
    
    # 支持通过命令行参数指定配置文件
    CONFIG_ARG=""
    if [ ! -z "$env" ]; then
        CONFIG_ARG="--config configs/config.$env.yaml"
        echo "使用环境配置文件: configs/config.$env.yaml"
    fi
    
    nohup "$EXECUTABLE" $CONFIG_ARG > "$LOG_FILE" 2>&1 &
    PID=$!
    echo "$PID" > "$PID_FILE"
    echo "服务启动成功，PID: $PID"
}

# 停止服务
stop() {
    if [ ! -f "$PID_FILE" ]; then
        echo "服务未运行"
        return
    fi

    PID=$(cat "$PID_FILE")
    if ps -p "$PID" > /dev/null 2>&1; then
        echo "正在停止服务，PID: $PID"
        kill "$PID"
        if [ $? -eq 0 ]; then
            echo "服务停止成功"
            rm "$PID_FILE"
        else
            echo "服务停止失败"
        fi
    else
        echo "服务未运行，删除PID文件"
        rm "$PID_FILE"
    fi
}

# 重启服务
restart() {
    stop
    sleep 2
    start "$@"
}

# 查看服务状态
status() {
    if [ -f "$PID_FILE" ]; then
        PID=$(cat "$PID_FILE")
        if ps -p "$PID" > /dev/null 2>&1; then
            echo "服务正在运行，PID: $PID"
        else
            echo "服务未运行，但存在PID文件"
        fi
    else
        echo "服务未运行"
    fi
}

# 查看日志
logs() {
    if [ -f "$LOG_FILE" ]; then
        tail -f "$LOG_FILE"
    else
        echo "日志文件不存在"
    fi
}

# 帮助信息
help() {
    echo "使用方法: $0 [命令] [参数]"
    echo "命令列表:"
    echo "  build     - 编译项目"
    echo "  start     - 启动服务，支持参数: -env=环境名称 (如: -env=prod)"
    echo "  stop      - 停止服务"
    echo "  restart   - 重启服务"
    echo "  status    - 查看服务状态"
    echo "  logs      - 查看日志"
    echo "  help      - 显示帮助信息"
}

# 主函数
main() {
    case "$1" in
        build)
            build
            ;;
        start)
            start "$@"
            ;;
        stop)
            stop
            ;;
        restart)
            restart "$@"
            ;;
        status)
            status
            ;;
        logs)
            logs
            ;;
        help)
            help
            ;;
        *)
            echo "未知命令: $1"
            help
            ;;
    esac
}

# 执行主函数
main "$@"
