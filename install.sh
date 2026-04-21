#!/bin/bash
# ============================================================
# CentOS 7.9 自动安装 Go + MySQL 8.0 脚本
# 用法: chmod +x install.sh && sudo ./install.sh
# ============================================================

set -e

# ==================== 配置区 ====================
GO_VERSION="1.22.5"
MYSQL_VERSION="8.0"
MYSQL_ROOT_PASSWORD="Root2026abc"
MYSQL_DATA_DIR="/data/mysql"
MYSQL_PORT=3306
GO_INSTALL_DIR="/usr/local"
GOPATH="/home/gopath"
# ================================================

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# 检查 root 权限
if [ "$(id -u)" -ne 0 ]; then
    log_error "请使用 root 用户或 sudo 执行此脚本"
    exit 1
fi

# 检查系统
if ! grep -q "CentOS" /etc/redhat-release 2>/dev/null; then
    log_warn "当前系统不是 CentOS，脚本可能不完全兼容"
fi

log_info "=========================================="
log_info "  CentOS 7.9 Go + MySQL 自动安装脚本"
log_info "  Go 版本: ${GO_VERSION}"
log_info "  MySQL 版本: ${MYSQL_VERSION}"
log_info "=========================================="

# ==================== 安装基础依赖 ====================
log_info "安装基础依赖..."
yum install -y wget curl tar gcc make net-tools libaio numactl perl > /dev/null 2>&1
log_info "基础依赖安装完成"

# ==================== 安装 Go ====================
install_go() {
    log_info "====== 开始安装 Go ${GO_VERSION} ======"

    # 检查是否已安装
    if command -v go &> /dev/null; then
        CURRENT_GO=$(go version 2>/dev/null | awk '{print $3}' | sed 's/go//')
        if [ "$CURRENT_GO" = "$GO_VERSION" ]; then
            log_info "Go ${GO_VERSION} 已安装，跳过"
            return 0
        else
            log_warn "发现旧版本 Go ${CURRENT_GO}，将替换为 ${GO_VERSION}"
            rm -rf ${GO_INSTALL_DIR}/go
        fi
    fi

    GO_TAR="go${GO_VERSION}.linux-amd64.tar.gz"
    GO_URL="https://go.dev/dl/${GO_TAR}"

    # 国内镜像备用
    GO_URL_CN="https://golang.google.cn/dl/${GO_TAR}"

    cd /tmp

    # 下载
    log_info "下载 Go ${GO_VERSION}..."
    if ! wget -q --timeout=30 "${GO_URL}" -O "${GO_TAR}" 2>/dev/null; then
        log_warn "官方源下载失败，尝试国内镜像..."
        wget -q --timeout=30 "${GO_URL_CN}" -O "${GO_TAR}" || {
            log_error "Go 下载失败"
            return 1
        }
    fi

    # 解压安装
    log_info "解压安装 Go..."
    tar -C ${GO_INSTALL_DIR} -xzf "${GO_TAR}"
    rm -f "${GO_TAR}"

    # 创建 GOPATH
    mkdir -p ${GOPATH}/{src,pkg,bin}

    # 配置环境变量
    cat > /etc/profile.d/go.sh << 'GOEOF'
export GOROOT=/usr/local/go
export GOPATH=/home/gopath
export PATH=$GOROOT/bin:$GOPATH/bin:$PATH
export GO111MODULE=on
export GOPROXY=https://goproxy.cn,direct
GOEOF

    source /etc/profile.d/go.sh

    # 验证
    if go version &> /dev/null; then
        log_info "Go 安装成功: $(go version)"
    else
        log_error "Go 安装失败"
        return 1
    fi
}

# ==================== 安装 MySQL ====================
install_mysql() {
    log_info "====== 开始安装 MySQL ${MYSQL_VERSION} ======"

    # 检查是否已安装
    if command -v mysqld &> /dev/null; then
        CURRENT_MYSQL=$(mysqld --version 2>/dev/null | awk '{print $3}')
        log_warn "发现已安装 MySQL ${CURRENT_MYSQL}"
        read -p "是否继续安装（会跳过已安装的）? [y/N]: " confirm
        if [ "$confirm" != "y" ] && [ "$confirm" != "Y" ]; then
            log_info "跳过 MySQL 安装"
            return 0
        fi
    fi

    # 停止并卸载旧版 MariaDB
    if rpm -q mariadb-libs &> /dev/null; then
        log_info "卸载系统自带 MariaDB..."
        systemctl stop mariadb 2>/dev/null || true
        yum remove -y mariadb mariadb-libs mariadb-server > /dev/null 2>&1 || true
    fi

    # 添加 MySQL Yum 仓库
    log_info "添加 MySQL 官方 Yum 仓库..."
    MYSQL_REPO_RPM="mysql80-community-release-el7-11.noarch.rpm"
    MYSQL_REPO_URL="https://dev.mysql.com/get/${MYSQL_REPO_RPM}"

    if ! rpm -q mysql80-community-release &> /dev/null; then
        cd /tmp
        wget -q --timeout=30 "${MYSQL_REPO_URL}" -O "${MYSQL_REPO_RPM}" || {
            log_error "MySQL 仓库 RPM 下载失败"
            return 1
        }
        rpm -ivh "${MYSQL_REPO_RPM}" > /dev/null 2>&1
        rm -f "${MYSQL_REPO_RPM}"
    fi

    # 导入 GPG 密钥
    rpm --import https://repo.mysql.com/RPM-GPG-KEY-mysql-2023 2>/dev/null || true
    rpm --import https://repo.mysql.com/RPM-GPG-KEY-mysql-2022 2>/dev/null || true

    # 安装 MySQL Server
    log_info "安装 MySQL Server（可能需要几分钟）..."
    yum install -y mysql-community-server mysql-community-client || {
        # 如果 GPG 校验失败，禁用 GPG 重试
        log_warn "GPG 校验失败，尝试跳过..."
        yum install -y --nogpgcheck mysql-community-server mysql-community-client || {
            log_error "MySQL 安装失败"
            return 1
        }
    }

    # 创建数据目录
    log_info "配置 MySQL 数据目录: ${MYSQL_DATA_DIR}"
    mkdir -p ${MYSQL_DATA_DIR}
    chown -R mysql:mysql ${MYSQL_DATA_DIR}

    # 备份原配置
    [ -f /etc/my.cnf ] && cp /etc/my.cnf /etc/my.cnf.bak

    # 写入配置文件
    cat > /etc/my.cnf << MYCNF
[mysqld]
# 基础配置
port=${MYSQL_PORT}
datadir=${MYSQL_DATA_DIR}
socket=/var/lib/mysql/mysql.sock
pid-file=/var/run/mysqld/mysqld.pid
log-error=/var/log/mysqld.log

# 字符集
character-set-server=utf8mb4
collation-server=utf8mb4_unicode_ci

# 安全配置 - 仅允许本地访问
bind-address=127.0.0.1
skip-name-resolve

# 性能优化
innodb_buffer_pool_size=256M
innodb_log_file_size=64M
innodb_flush_log_at_trx_commit=2
max_connections=200
max_allowed_packet=64M
tmp_table_size=64M
max_heap_table_size=64M

# 慢查询日志
slow_query_log=1
slow_query_log_file=/var/log/mysql-slow.log
long_query_time=2

# 默认认证插件（兼容旧客户端）
default-authentication-plugin=mysql_native_password

[client]
default-character-set=utf8mb4
socket=/var/lib/mysql/mysql.sock

[mysql]
default-character-set=utf8mb4
MYCNF

    # 初始化数据目录（如果为空）
    if [ ! -d "${MYSQL_DATA_DIR}/mysql" ]; then
        log_info "初始化 MySQL 数据目录..."
        mysqld --initialize --user=mysql --datadir=${MYSQL_DATA_DIR}
    fi

    # 创建 PID 目录
    mkdir -p /var/run/mysqld
    chown mysql:mysql /var/run/mysqld

    # 启动 MySQL
    log_info "启动 MySQL..."
    systemctl start mysqld
    systemctl enable mysqld

    # 获取临时密码
    TEMP_PASSWORD=$(grep 'temporary password' /var/log/mysqld.log | tail -1 | awk '{print $NF}')
    log_info "MySQL 临时密码: ${TEMP_PASSWORD}"

    # 等待 MySQL 完全启动
    sleep 3

    # 修改 root 密码
    log_info "设置 root 密码..."
    mysql --connect-expired-password -u root -p"${TEMP_PASSWORD}" -h 127.0.0.1 << SQLEOF 2>/dev/null
ALTER USER 'root'@'localhost' IDENTIFIED BY '${MYSQL_ROOT_PASSWORD}';
FLUSH PRIVILEGES;
SQLEOF

    if [ $? -ne 0 ]; then
        # 如果密码策略阻止，先降低策略再设置
        log_warn "尝试降低密码策略后重试..."
        mysql --connect-expired-password -u root -p"${TEMP_PASSWORD}" -h 127.0.0.1 << SQLEOF2 2>/dev/null
SET GLOBAL validate_password.policy=LOW;
SET GLOBAL validate_password.length=8;
ALTER USER 'root'@'localhost' IDENTIFIED BY '${MYSQL_ROOT_PASSWORD}';
FLUSH PRIVILEGES;
SQLEOF2
    fi

    # 安全加固：删除匿名用户和测试数据库
    log_info "安全加固..."
    mysql -u root -p"${MYSQL_ROOT_PASSWORD}" -h 127.0.0.1 << SQLEOF3 2>/dev/null
DELETE FROM mysql.user WHERE User='';
DELETE FROM mysql.user WHERE User='root' AND Host NOT IN ('localhost', '127.0.0.1', '::1');
DROP DATABASE IF EXISTS test;
DELETE FROM mysql.db WHERE Db='test' OR Db='test\\_%';
FLUSH PRIVILEGES;
SQLEOF3

    # 创建应用数据库
    log_info "创建应用数据库 google_map..."
    mysql -u root -p"${MYSQL_ROOT_PASSWORD}" -h 127.0.0.1 << SQLEOF4 2>/dev/null
CREATE DATABASE IF NOT EXISTS google_map DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
SQLEOF4

    # 验证
    if mysqladmin -u root -p"${MYSQL_ROOT_PASSWORD}" -h 127.0.0.1 ping &> /dev/null; then
        log_info "MySQL 安装并启动成功"
        log_info "MySQL 版本: $(mysql -V)"
    else
        log_error "MySQL 启动验证失败"
        return 1
    fi
}

# ==================== 配置防火墙 ====================
configure_firewall() {
    log_info "配置防火墙..."

    if systemctl is-active firewalld &> /dev/null; then
        # 不暴露 MySQL 端口（仅本地访问）
        # 如果需要远程访问 MySQL，取消下面注释：
        # firewall-cmd --permanent --add-port=${MYSQL_PORT}/tcp

        firewall-cmd --reload > /dev/null 2>&1
        log_info "防火墙配置完成（MySQL 仅本地访问，未开放外部端口）"
    else
        log_warn "firewalld 未运行，跳过防火墙配置"
    fi
}

# ==================== 执行安装 ====================
install_go
install_mysql
configure_firewall

# ==================== 安装完成汇总 ====================
echo ""
echo "=========================================="
log_info "安装完成！汇总信息："
echo "=========================================="
echo ""
echo "  Go:"
echo "    版本:     $(source /etc/profile.d/go.sh && go version 2>/dev/null || echo '需要重新登录')"
echo "    GOROOT:   ${GO_INSTALL_DIR}/go"
echo "    GOPATH:   ${GOPATH}"
echo "    代理:     https://goproxy.cn"
echo ""
echo "  MySQL:"
echo "    版本:     $(mysql -V 2>/dev/null || echo 'N/A')"
echo "    端口:     ${MYSQL_PORT}"
echo "    数据目录: ${MYSQL_DATA_DIR}"
echo "    绑定地址: 127.0.0.1（仅本地）"
echo "    root密码: ${MYSQL_ROOT_PASSWORD}"
echo "    数据库:   google_map"
echo ""
echo "  常用命令:"
echo "    source /etc/profile.d/go.sh    # 加载 Go 环境（或重新登录）"
echo "    systemctl status mysqld         # 查看 MySQL 状态"
echo "    systemctl restart mysqld        # 重启 MySQL"
echo "    mysql -u root -p -h 127.0.0.1  # 连接 MySQL"
echo ""
echo "  下一步："
echo "    1. 执行 source /etc/profile.d/go.sh 加载 Go 环境"
echo "    2. cd 到项目目录执行 go run ."
echo ""
echo "=========================================="
