# Nâng cấp GửiWeb lên v0.1.3

v0.1.3 bổ sung kho khóa API cho từng website, relay Telegram, dán HTML để
tạo/cập nhật và prompt có thể sao chép để tự dùng với AI.

- Tab **Khóa API** chỉ quản lý site thuộc chính chủ tài khoản, kể cả admin.
  Tối đa 20 tên/site theo mẫu `[A-Z][A-Z0-9_]{0,63}`; giá trị mã hóa AES-GCM,
  không được trả lại dạng rõ. Có thể thay khóa hoặc thu hồi.
- **Telegram Bot** dùng bot token và chat đích cố định do chủ site nhập trong
  UI. `POST /__guiweb/telegram/TEN_KHOA` trên chính site public nhận JSON
  `{ "text": "..." }`, yêu cầu Origin đúng, không CORS. Đây là endpoint cho
  khách truy cập, không xác thực người gửi riêng. Chỉ gọi Telegram
  `sendMessage` với văn bản thường; không nhận token/chat/URL từ client.
- Ratelimit relay: 5 tin/phút/IP khách + site, 20 tin/phút/site,
  60 tin/phút toàn hệ thống; 4 request đồng thời toàn hệ thống, timeout 10 giây.
  **Khóa khác (chỉ lưu giữ)** chưa có proxy API đa mục đích.
- **Dán HTML** lưu `index.html`, giữ link khi cập nhật và áp dụng quota cũ.
  Cập nhật HTML thay toàn bộ file của bản trước.
- **Tạo với AI** chỉ tạo và sao chép prompt trên trình duyệt. Bạn tự dùng
  công cụ AI rồi dán HTML kết quả; không tích hợp gọi AI trả phí hoặc chạy
  backend tùy ý.

Hướng dẫn dành cho router `aarch64` đã cài v0.1.0–v0.1.2 với binary
`/usr/bin/guiweb`, service `/etc/init.d/guiweb`, config
`/etc/guiweb/config.json` và dữ liệu `/overlay/guiweb`. Trang quản lý dùng
`app.phatnt.id.vn`, listener `127.0.0.1:8088`. Nếu đã đổi hostname/listener,
chỉnh hai biến đầu đoạn lệnh trước khi chạy.

Chỉ thay binary; giữ config, DB, tài khoản, website và Tunnel. Không tạo lại
admin hoặc reset mật khẩu. Bản mới tự thêm bảng khóa, giữ schema version 1.
Với dữ liệu v0.1.2 chưa có secret, service tự sinh master key
`<data_dir>/.secrets-key` quyền 600 ở lần chạy đầu; tài khoản service cần
quyền ghi data_dir như trước. Không tự tạo hoặc chép key mẫu vào thư mục.

Giữ `.secrets-key` cùng DB khi sao lưu hoặc khôi phục. Nếu mất key khi đã
lưu secret, các khóa không thể giải mã và service từ chối khởi động; không
thay bằng key mới để bỏ qua lỗi. Nâng cấp và quay lại binary không xóa hoặc
thay file key. Khi sao lưu thủ công, dừng service và lưu cả thư mục dữ liệu,
bao gồm file ẩn và DB; không copy riêng DB đang chạy WAL.

DB vẫn tương thích binary cũ, không cần khôi phục DB khi quay lại. Bản cũ
không dùng vault hoặc `.secrets-key`; giữ file này để chạy lại v0.1.3.
Nếu quay về v0.1.0/v0.1.1, link đăng ký nhiều lượt tạm không dùng được nhưng
số lượt còn giữ; dùng lại trên bản mới nếu còn hạn/lượt và chưa thu hồi.

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
GUIWEB_RELEASE_URL='https://github.com/NguyenPhat-NT/chia_se_web/releases/download/v0.1.3'
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

GUIWEB_UPGRADE_DIR=$(mktemp -d /tmp/guiweb-upgrade-0.1.3.XXXXXX)
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

GUIWEB_BACKUP_DIR=$(mktemp -d /overlay/guiweb-backup-before-0.1.3.XXXXXX)
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
printf 'GửiWeb v0.1.3 đã đạt readiness. Bản lưu cũ: %s\n' "$GUIWEB_BACKUP_DIR"
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

Mở `https://app.phatnt.id.vn`, đăng nhập bằng tài khoản hiện tại. Tải lại
một lần bằng **Ctrl+Shift+R** để nhận giao diện mới và xác nhận website cũ
còn hoạt động.

1. Trong hộp tải lên chọn **Dán HTML**, dán **Mã HTML đầy đủ** và tạo site.
   Cập nhật bằng một HTML khác, xác nhận giữ link và chỉ có `index.html`
   mới; các file của bản cũ không tự giữ lại.
2. Mở **Tạo với AI**, nhập **Bạn muốn làm website gì?**, dùng **Tạo lại
   prompt** và **Sao chép prompt**. Kiểm tra clipboard có hướng dẫn tương
   ứng; thao tác này không gọi dịch vụ AI trả phí.
3. Mở **Khóa API**, chọn **Website của bạn**. Site của tài khoản khác không
   xuất hiện dù tài khoản hiện tại là admin. Kiểm tra API không trả giá trị
   khóa rõ khi mở danh sách hoặc chọn **Thay khóa**.
4. Telegram thật do chủ site tự cấu hình token và **Chat ID nhận tin** trong
   UI khi sử dụng. Khi kiểm thử tự động, dùng Telegram mock để xác nhận
   chat cố định, Origin và rate limit; không dùng token thật trong bộ test.
5. Kiểm tra các chức năng đã có: link đăng ký, upload file/thư mục, đổi tên,
   cập nhật cùng link, xóa và dùng lại slug; `in4.phatnt.id.vn` vẫn hoạt động.

Router cần DNS, HTTPS cổng 443 và CA hợp lệ để gọi `api.telegram.org`.
Endpoint relay dùng hostname website hiện có, không cần đổi route Tunnel.
Giữ bot token ngoài HTML/JS public, prompt AI, Git, log và lệnh shell.

Readiness kiểm tra DB và thư mục dữ liệu; chức năng, relay và HTTPS trên
thiết bị thật cần kiểm chứng riêng như trên. Tài liệu này không xác nhận
router đã được nâng cấp hoặc đã gửi Telegram thành công.

## 3. Quay lại thủ công khi cần

Nếu muốn quay lại sau khi nâng cấp thành công, thay `ABC123` bằng đúng phần
cuối đường dẫn bản lưu mà script đã in. Chạy trong SSH root:

```sh
sh <<'GUIWEB_ROLLBACK'
set -eu
GUIWEB_BACKUP_DIR='/overlay/guiweb-backup-before-0.1.3.ABC123'
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

Config và DB hiện tại vẫn dùng được với binary cũ; không chép config mẫu,
không xóa `/overlay/guiweb`, không khôi phục DB và giữ `.secrets-key`.
Bản cũ không có vault/relay mới. Nếu đổi hostname/listener, đổi lệnh health
tương ứng. Chạy lại v0.1.3 dùng chính file key đã có để giải mã các secret.
