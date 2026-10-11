# Nâng cấp GửiWeb từ v0.1.0 lên v0.1.1

v0.1.1 cho phép bất kỳ thành viên nào dùng lại slug của website đã xóa, kể
cả những tên bị giữ lại từ v0.1.0. Tên đang có website vẫn duy nhất; các tên
dành riêng như `app`, `in4` vẫn bị chặn. Khi dùng lại slug, link cũ hiển thị
website mới. Xóa trên router không xóa `localStorage` hoặc service worker
đã lưu trên trình duyệt người xem.

Giao diện v0.1.1 cũng tự nhận dạng HTML/ZIP khi chọn file và nội dung khi
chọn thư mục, cho cả tạo mới lẫn cập nhật. Không cần chọn loại upload thủ công.

Hướng dẫn dành cho router `aarch64` đã cài `/usr/bin/guiweb`, service
`/etc/init.d/guiweb`, config `/etc/guiweb/config.json` và dữ liệu
`/overlay/guiweb`. Trang quản lý dùng `app.phatnt.id.vn`, listener
`127.0.0.1:8088`. Nếu đã đổi hostname hoặc listener, chỉnh hai biến đầu
đoạn lệnh bên dưới trước khi chạy.

Chỉ thay binary; giữ config, DB, tài khoản và website. Không tạo admin mới,
reset mật khẩu, xóa dữ liệu hoặc thay Tunnel. Khi khởi động, bản mới dọn
các tên giữ chỗ không còn website. Schema DB vẫn ở phiên bản 1, tương thích
v0.1.0; quay lại binary cũ không cần khôi phục DB. Quay lại v0.1.0 cũng quay
lại hành vi giữ tên đối với các website xóa sau đó.

## 1. Tải, kiểm tra và nâng cấp

Đăng nhập SSH bằng `root`. Đoạn nâng cấp tự động cần `curl`, `sha256sum`,
`tar`, `mktemp`, `pidof`. Nếu chưa có `curl`, cài bằng package manager và feed
đang dùng trên router; không suy ra `apk` hay `opkg` từ tên firmware:

```sh
sh <<'GUIWEB_CURL'
set -eu
if ! command -v curl >/dev/null 2>&1; then
  if command -v apk >/dev/null 2>&1; then
    apk update
    apk add curl ca-bundle
  elif command -v opkg >/dev/null 2>&1; then
    opkg update
    opkg install curl ca-bundle
  else
    printf 'Không tìm thấy apk/opkg; cần cài curl bằng cách quản lý package hiện có.\n' >&2
    exit 1
  fi
fi
GUIWEB_CURL
```

Nếu cài package thất bại, xử lý lỗi đó trước khi chạy nâng cấp. Khi đủ các
lệnh, chạy nguyên đoạn dưới trong terminal SSH. Gói được tải vào thư mục tạm riêng quyền 700;
binary/config cũ được lưu vào thư mục riêng bền vững dưới `/overlay`.

```sh
sh <<'GUIWEB_UPGRADE'
set -eu
umask 077
GUIWEB_ADMIN_HOST='app.phatnt.id.vn'
GUIWEB_LISTENER='http://127.0.0.1:8088'
GUIWEB_RELEASE_URL='https://github.com/NguyenPhat-NT/chia_se_web/releases/download/v0.1.1'
GUIWEB_RESTORE_REQUIRED=0
GUIWEB_ATOMIC_BIN=''
GUIWEB_BACKUP_DIR=''

[ "$(id -u)" -eq 0 ] || { printf 'Cần chạy bằng root.\n' >&2; exit 1; }
[ "$(uname -m)" = aarch64 ] || { printf 'Binary này cần aarch64.\n' >&2; exit 1; }
for GUIWEB_COMMAND in id uname curl sha256sum tar mktemp cp chmod chown mv pidof sleep rm; do
  command -v "$GUIWEB_COMMAND" >/dev/null 2>&1 || {
    printf 'Thiếu lệnh: %s\n' "$GUIWEB_COMMAND" >&2
    exit 1
  }
done
[ -x /usr/bin/guiweb ] && [ -x /etc/init.d/guiweb ] && [ -r /etc/guiweb/config.json ] || {
  printf 'Chưa thấy đủ binary, service và config đang cài.\n' >&2
  exit 1
}

guiweb_stop() {
  /etc/init.d/guiweb stop || return 1
  GUIWEB_STOP_TRIES=0
  while pidof guiweb >/dev/null 2>&1; do
    GUIWEB_STOP_TRIES=$((GUIWEB_STOP_TRIES + 1))
    [ "$GUIWEB_STOP_TRIES" -lt 40 ] || {
      printf 'GửiWeb chưa dừng; không thay binary đang chạy.\n' >&2
      return 1
    }
    sleep 1 || return 1
  done
}

guiweb_ready() {
  GUIWEB_READY_TRIES=0
  while [ "$GUIWEB_READY_TRIES" -lt 15 ]; do
    if GUIWEB_HEALTH=$(curl --fail --silent --show-error --connect-timeout 1 --max-time 2 \
        -H "Host: $GUIWEB_ADMIN_HOST" "$GUIWEB_LISTENER/api/health" 2>/dev/null); then
      case "$GUIWEB_HEALTH" in
        *'"status":"ok"'*) printf '%s\n' "$GUIWEB_HEALTH"; return 0 ;;
      esac
    fi
    GUIWEB_READY_TRIES=$((GUIWEB_READY_TRIES + 1))
    sleep 1 || return 1
  done
  return 1
}

guiweb_replace_binary() {
  if [ -n "$GUIWEB_ATOMIC_BIN" ]; then
    rm -f "$GUIWEB_ATOMIC_BIN" || return 1
    GUIWEB_ATOMIC_BIN=''
  fi
  GUIWEB_ATOMIC_BIN=$(mktemp /usr/bin/.guiweb-upgrade.XXXXXX) || return 1
  cp "$1" "$GUIWEB_ATOMIC_BIN" || return 1
  chown root:root "$GUIWEB_ATOMIC_BIN" || return 1
  chmod 755 "$GUIWEB_ATOMIC_BIN" || return 1
  GUIWEB_SOURCE_SUM=$(sha256sum "$1") || return 1
  GUIWEB_TARGET_SUM=$(sha256sum "$GUIWEB_ATOMIC_BIN") || return 1
  [ "${GUIWEB_SOURCE_SUM%% *}" = "${GUIWEB_TARGET_SUM%% *}" ] || return 1
  mv -f "$GUIWEB_ATOMIC_BIN" /usr/bin/guiweb || return 1
  GUIWEB_ATOMIC_BIN=''
}

guiweb_restore() {
  guiweb_stop || return 1
  guiweb_replace_binary "$GUIWEB_BACKUP_DIR/guiweb" || return 1
  /etc/init.d/guiweb start || return 1
  guiweb_ready
}

guiweb_exit() {
  GUIWEB_RESULT=$?
  trap - 0 HUP INT TERM
  if [ "$GUIWEB_RESULT" -ne 0 ] && [ "$GUIWEB_RESTORE_REQUIRED" -eq 1 ]; then
    printf 'Nâng cấp lỗi hoặc chưa đạt readiness, đang quay lại binary cũ.\n' >&2
    if guiweb_restore; then
      printf 'Đã chạy lại binary cũ; DB và config được giữ nguyên.\n' >&2
    else
      printf 'Chưa xác nhận bản cũ chạy lại. Bản lưu: %s\n' "$GUIWEB_BACKUP_DIR" >&2
      printf 'Kiểm tra: logread -e guiweb\n' >&2
    fi
  fi
  if [ -n "$GUIWEB_ATOMIC_BIN" ]; then rm -f "$GUIWEB_ATOMIC_BIN"; fi
  exit "$GUIWEB_RESULT"
}
trap guiweb_exit 0
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

GUIWEB_UPGRADE_DIR=$(mktemp -d /tmp/guiweb-upgrade-0.1.1.XXXXXX)
cd "$GUIWEB_UPGRADE_DIR"
curl --fail --location --silent --show-error --proto '=https' --proto-redir '=https' \
  "$GUIWEB_RELEASE_URL/guiweb-openwrt-arm64.tar.gz" -o guiweb-openwrt-arm64.tar.gz
curl --fail --location --silent --show-error --proto '=https' --proto-redir '=https' \
  "$GUIWEB_RELEASE_URL/guiweb-openwrt-arm64.tar.gz.sha256" -o guiweb-openwrt-arm64.tar.gz.sha256
sha256sum -c guiweb-openwrt-arm64.tar.gz.sha256
tar -xzf guiweb-openwrt-arm64.tar.gz
sha256sum -c guiweb-arm64.sha256
chmod 755 guiweb-linux-arm64
./guiweb-linux-arm64 version

GUIWEB_BACKUP_DIR=$(mktemp -d /overlay/guiweb-backup-0.1.0.XXXXXX)
cp -p /usr/bin/guiweb "$GUIWEB_BACKUP_DIR/guiweb"
cp -p /etc/guiweb/config.json "$GUIWEB_BACKUP_DIR/config.json"
printf 'Bản lưu binary/config cũ: %s\n' "$GUIWEB_BACKUP_DIR"

GUIWEB_RESTORE_REQUIRED=1
guiweb_stop
guiweb_replace_binary "$GUIWEB_UPGRADE_DIR/guiweb-linux-arm64"
/etc/init.d/guiweb start
if ! guiweb_ready; then
  printf 'Bản mới chưa đạt readiness.\n' >&2
  exit 1
fi
GUIWEB_RESTORE_REQUIRED=0
printf 'GửiWeb v0.1.1 đã đạt readiness. Bản lưu cũ: %s\n' "$GUIWEB_BACKUP_DIR"
printf 'File tải xuống: %s\n' "$GUIWEB_UPGRADE_DIR"
GUIWEB_UPGRADE
```

SHA-256 của gói và binary được kiểm tra trước khi dừng service. Binary được
chép vào file tạm trong `/usr/bin`, đặt `root:root`, mode 755 rồi rename.
Nếu readiness thất bại, script thử quay lại binary cũ và chạy lại service;
khi đó lệnh vẫn báo lỗi nâng cấp, dù việc quay lại thành công. Config mẫu
trong gói không được áp dụng; DB không được copy, xóa hoặc khôi phục.
Giữ thư mục bản lưu đến khi đã kiểm tra sử dụng thực tế.

## 2. Kiểm tra sau nâng cấp

Mở `https://app.phatnt.id.vn`, đăng nhập bằng tài khoản hiện tại và kiểm tra
website cũ còn hoạt động. Tạo website thử, xóa rồi tạo lại cùng slug bằng
một thành viên khác: link phải phục vụ website mới. Những slug đã xóa từ
v0.1.0 cũng dùng lại được sau lần khởi động mới.

Thử chọn một HTML hoặc ZIP bằng nút chọn file, và một thư mục bằng nút chọn
thư mục. Giao diện phải tự nhận dạng nội dung trước khi gửi; kiểm tra cả tạo
website mới và cập nhật website hiện có.

Tên đang dùng hoặc tên dành riêng vẫn phải bị từ chối. Nếu trình duyệt còn
hiển thị nội dung cũ, xóa dữ liệu trang/service worker của hostname đó rồi
kiểm tra lại. Xác nhận `in4.phatnt.id.vn` vẫn hoạt động.

Readiness chứng minh DB và thư mục dữ liệu dùng được; thao tác ứng dụng và
HTTPS trên thiết bị thật cần kiểm tra như trên. Tài liệu này không xác nhận
router đã được nâng cấp.

## 3. Quay lại thủ công khi cần

Nếu muốn quay lại sau khi nâng cấp thành công, thay `ABC123` bằng đúng phần
cuối đường dẫn bản lưu mà script đã in. Chạy trong SSH root:

```sh
sh <<'GUIWEB_ROLLBACK'
set -eu
GUIWEB_BACKUP_DIR='/overlay/guiweb-backup-0.1.0.ABC123'
[ -x "$GUIWEB_BACKUP_DIR/guiweb" ] || { printf 'Không tìm thấy binary cũ.\n' >&2; exit 1; }
/etc/init.d/guiweb stop
GUIWEB_STOP_TRIES=0
while pidof guiweb >/dev/null 2>&1; do
  GUIWEB_STOP_TRIES=$((GUIWEB_STOP_TRIES + 1))
  [ "$GUIWEB_STOP_TRIES" -lt 40 ] || { printf 'GửiWeb chưa dừng.\n' >&2; exit 1; }
  sleep 1
done
GUIWEB_OLD_BIN=$(mktemp /usr/bin/.guiweb-rollback.XXXXXX)
trap 'rm -f "$GUIWEB_OLD_BIN"' 0
cp "$GUIWEB_BACKUP_DIR/guiweb" "$GUIWEB_OLD_BIN"
chown root:root "$GUIWEB_OLD_BIN"
chmod 755 "$GUIWEB_OLD_BIN"
mv -f "$GUIWEB_OLD_BIN" /usr/bin/guiweb
/etc/init.d/guiweb start
GUIWEB_READY_TRIES=0
GUIWEB_READY_OK=0
while [ "$GUIWEB_READY_TRIES" -lt 15 ]; do
  if GUIWEB_HEALTH=$(curl --fail --silent --show-error --connect-timeout 1 --max-time 2 \
      -H 'Host: app.phatnt.id.vn' http://127.0.0.1:8088/api/health 2>/dev/null); then
    case "$GUIWEB_HEALTH" in
      *'"status":"ok"'*) printf '%s\n' "$GUIWEB_HEALTH"; GUIWEB_READY_OK=1; break ;;
    esac
  fi
  GUIWEB_READY_TRIES=$((GUIWEB_READY_TRIES + 1))
  sleep 1
done
[ "$GUIWEB_READY_OK" -eq 1 ] || {
  printf 'Binary cũ chưa đạt readiness; kiểm tra logread -e guiweb.\n' >&2
  exit 1
}
GUIWEB_ROLLBACK
```

Config và DB hiện tại vẫn dùng được với binary v0.1.0; không chép config
mẫu, không xóa `/overlay/guiweb` và không khôi phục DB. Nếu đã đổi hostname
hoặc listener, đổi lệnh health tương ứng.
