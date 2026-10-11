# 沙箱执行镜像(17-脚本沙箱技术方案 §5.1)。
# 用途:sandbox_run 工具的一次性执行容器——默认断网、rootfs 只读、非 root,由执行器保证;
# 本镜像只负责"预装什么运行时与库",按需增删后重建:make sandbox-image
FROM python:3.12-slim

# 常用数据处理库(按需增删;重建即可,不进运行时参数)
RUN pip install --no-cache-dir numpy pandas requests openpyxl

# 非 root 用户(uid 1000,与执行器硬编码的 sandboxUser 对齐)
RUN useradd -m -u 1000 app

USER app
WORKDIR /workspace
