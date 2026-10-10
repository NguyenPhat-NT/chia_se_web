# Kế hoạch triển khai GửiWeb

Trạng thái: các mốc phát triển và kiểm chứng cloud đã thực hiện. Chưa cài
ứng dụng hoặc thay đổi router. Các hạn mức trong phần kế hoạch ban đầu được
điều chỉnh theo chẩn đoán thiết bị; xem phần 10 và cấu hình hiện hành.

## 1. Mục tiêu và phạm vi bản đầu

GửiWeb cho một nhóm người quen tải website tĩnh lên và nhận link công khai.
Máy chủ đích là JDCloud RE-SS-01 chạy Kwrt/OpenWrt, RAM 512 MB, bộ nhớ
trống khoảng 7 GB theo thông tin chủ thiết bị. Router đã có dịch vụ public
qua Cloudflare Tunnel. Ảnh thiết bị cho thấy ARMv8 và nền tảng ipq60xx;
kiến trúc binary phải được xác nhận trên hệ điều hành trước khi build.

- Chủ hệ thống gửi link mời một lần để người quen tự tạo tài khoản.
- Thành viên quản lý website của mình; quản trị viên quản lý tất cả.
- Người xem không cần đăng nhập.
- Nhận ZIP, thư mục và một file HTML; giữ nguyên cấu trúc tài nguyên.
- Hỗ trợ HTML/CSS/JavaScript và thư mục đã build của React/Vue.
- Đổi tên hiển thị, xóa, cập nhật nội dung và sao chép link.
- Tên hiển thị độc lập với slug; cập nhật hoặc đổi tên hiển thị không đổi link.
- Website dùng localStorage được, nhưng dữ liệu chỉ nằm trên từng trình duyệt.
- Chưa cung cấp PHP, backend tùy ý, API lưu dữ liệu chung, đăng ký công khai,
  tên miền riêng từng thành viên, thống kê truy cập hay sao lưu tự động.

Các hạn mức dưới đây là mặc định đề xuất, có thể cấu hình. Dùng MiB/GiB
trong cấu hình để tránh nhập nhằng với MB/GB:

| Hạn mức | Mặc định |
| --- | --- |
| Tổng dữ liệu file của mỗi lượt upload | 50 MiB |
| Tổng dung lượng website sau giải nén | 100 MiB |
| Số file mỗi website | 5.000 |
| Tổng dữ liệu ứng dụng, kể cả staging và bản cũ | 4 GiB |
| Dung lượng trống tối thiểu trước khi nhận upload | 1 GiB |
| Upload/xử lý đồng thời | 1 lượt đang xử lý; yêu cầu khác nhận thông báo thử lại |

Giới hạn HTTP body có phần dư nhỏ, hữu hạn cho multipart; không đọc toàn bộ
request hoặc ZIP vào RAM. Kiểm tra dung lượng thực tế khi ghi, không chỉ tin
metadata của ZIP. Mặc định giới hạn thành viên phải được chọn trước khi mở
cho nhóm, nằm trong hạn mức tổng; ban đầu đề xuất 500 MiB/người.

## 2. Kiến trúc

- Backend Go: một binary Linux, phục vụ API, giao diện quản lý và website tĩnh.
- SQLite lưu tài khoản, lời mời, phiên đăng nhập, website và phiên bản triển khai.
- Driver SQLite thuần Go để build không phụ thuộc CGO; chọn và pin phiên bản
  tương thích khi bắt đầu triển khai, kiểm chứng cả amd64 và arm64.
- Giao diện HTML/CSS/JavaScript, không cần runtime Node.js trên router.
- Cloudflare Tunnel hiện có chuyển lưu lượng về listener riêng của GửiWeb.
- Chạy dưới tài khoản dịch vụ riêng, chỉ có quyền ghi thư mục dữ liệu của ứng dụng.
- Mỗi task cloud dùng checkout hiện có; không tạo worktree nếu chưa được yêu cầu.

Ví dụ địa chỉ, cần thay bằng tên miền thực tế:

| Địa chỉ | Vai trò |
| --- | --- |
| `app.example.com` | Đăng nhập, lời mời và quản lý |
| `demo.example.com` | Website có slug `demo` |
| Các hostname không được cấu hình | Trả 404 |

Không tự tiếp nhận mọi subdomain của tên miền: cần danh sách hostname được
dành cho ứng dụng, tránh chiếm tên dịch vụ đang public. Dành trước ít nhất
`app`, `www`, `api`, `admin`; bổ sung các tên đang sử dụng. Slug chỉ chứa
chữ thường ASCII, số và dấu gạch nối; kiểm tra tính duy nhất khi tạo website.

Wildcard DNS và ingress của Tunnel cần cấu hình cùng nhau; chứng chỉ phải
bao phủ các hostname dự kiến. Nếu dùng một namespace sâu hơn như
`demo.sites.example.com`, kiểm tra riêng phạm vi chứng chỉ, không mặc định
chứng chỉ wildcard một cấp đã bao phủ. Có thể dùng cùng tunnel hiện có;
giữ nguyên các route khác và để route mặc định trả 404.

## 3. Dữ liệu và cập nhật

Thư mục dữ liệu đặt trên phân vùng bền vững đã xác nhận, ví dụ:

```text
<data-dir>/
  guiweb.sqlite
  sites/<site-id>/releases/<release-id>/
  staging/<upload-id>/
```

- Website có ID cố định, chủ sở hữu, tên hiển thị, slug và phiên bản hiện hành.
- Upload được ghi vào staging, kiểm tra rồi chuyển thành một release bất biến.
- Một ZIP có thư mục bọc ngoài duy nhất được bỏ lớp bọc nếu tìm thấy
  `index.html` đúng quy tắc; cấu trúc mơ hồ phải báo lỗi rõ ràng.
- Một file HTML đơn lẻ được lưu thành `index.html`; tài nguyên bên ngoài không
  được tự tải về hoặc tự ghép vào.
- Website thiếu `index.html` bị từ chối với hướng dẫn tải thư mục đã build.
- Khi cập nhật, chỉ chuyển con trỏ phiên bản sau khi bản mới hợp lệ; lỗi hoặc
  thiếu dung lượng giữ nguyên bản đang phục vụ.
- Đồng bộ metadata trong transaction; khởi động lại phải xử lý staging và
  release mồ côi, không xóa dữ liệu của một upload còn hoạt động.
- Bản cũ được dọn sau khoảng chờ ngắn để tránh làm hỏng request đang phục vụ;
  mọi bản giữ lại đều tính vào hạn mức. Giai đoạn ghi bản mới cần chỗ cho cả
  bản cũ lẫn bản mới, không chỉ chênh lệch dung lượng.
- Xóa website thu hồi link và dọn file; việc dọn còn dang dở được tiếp tục
  sau khởi động lại. Khôi phục do người dùng yêu cầu chưa thuộc bản đầu.

Phục vụ đường dẫn thư mục bằng `index.html`, không bật directory listing.
SPA có tùy chọn fallback về `index.html` cho điều hướng; mặc định tắt và
không che lỗi tài nguyên tĩnh không tồn tại. Không bật cache lâu cho HTML
hoặc tài nguyên dùng tên cũ, để cập nhật cùng link không bị giữ nội dung cũ.

## 4. Tài khoản và ranh giới truy cập

- Khởi tạo quản trị viên qua lệnh CLI, nhập mật khẩu tương tác; không nhúng
  mật khẩu vào mã nguồn, shell command, log hoặc cấu hình mẫu.
- Link mời chứa token ngẫu nhiên; DB chỉ lưu hash token. Link mặc định hết
  hạn sau 48 giờ, dùng một lần, quản trị viên có thể thu hồi.
- Link mời hiển thị để quản trị viên tự gửi; không cần tích hợp email.
- Hash mật khẩu bằng thuật toán thư viện chuẩn được duy trì; benchmark chi
  phí trên router trước khi chốt. Giới hạn thử đăng nhập và xử lý hash đồng thời.
- Session có hạn dùng, có thể thu hồi, cookie host-only `__Host-`, Secure,
  HttpOnly, SameSite. Không dùng cookie với Domain chung cho các subdomain.
- API quản lý chỉ nhận đúng hostname quản lý; kiểm tra quyền trên server
  cho từng website, không chỉ ẩn nút trong giao diện.
- Mọi thay đổi yêu cầu chống CSRF và kiểm tra Origin; không dựa riêng vào
  SameSite vì các website tải lên có thể nằm cùng site với trang quản lý.
- Không mở CORS cho website tải lên truy cập API quản lý. Hiển thị tên/file
  dưới dạng text an toàn, không chèn trực tiếp HTML người dùng.
- Không ghi mật khẩu, session, token mời hoặc query nhạy cảm vào access log.
- Cơ chế cấp lại quyền truy cập qua quản trị viên và CLI; chưa cần email reset.

## 5. Kiểm tra upload

- Chặn đường dẫn tuyệt đối, `..`, biến thể dấu phân cách, symlink và loại entry
  không phải thư mục/file thường; xác nhận đường dẫn đích luôn trong staging.
- Từ chối đường dẫn trùng sau chuẩn hóa, file cấu hình nhạy cảm và metadata
  như `.git`, `.env`; báo cụ thể khi archive không được chấp nhận.
- Kiểm tra giới hạn số file, độ dài đường dẫn, dung lượng thực và ZIP lỗi/mã hóa.
- Không thực thi script phía server, không chạy lệnh build từ nội dung upload.
- File được phục vụ với MIME phù hợp và `X-Content-Type-Options: nosniff`.
- Giới hạn thời gian request và tốc độ ghi cần benchmark, không đặt timeout
  quá ngắn khiến upload hợp lệ 50 MiB bị ngắt trên đường truyền thực tế.

## 6. Các mốc triển khai và tiêu chí hoàn thành

### Mốc 0 — Chuẩn bị môi trường phát triển

1. Cài Go chính thức, pin phiên bản, giữ kiểm tra TLS/checksum khi tải.
2. Khởi tạo module, cấu trúc backend/frontend, cấu hình mẫu không chứa bí mật.
3. Chọn driver SQLite, kiểm tra build native và cross-build cho kiến trúc đích.
4. Thêm hướng dẫn chạy, test và build; sau khi đã chạy thật, lưu install_script
   và start_skill vào cấu hình môi trường cloud nếu cần.

Hoàn thành khi có binary native chạy được và endpoint readiness kiểm tra
được DB cùng thư mục dữ liệu. Cross-build thành công chưa chứng minh router chạy được.

### Mốc 1 — Quản lý thành viên

Xây CLI tạo admin, đăng nhập/đăng xuất, lời mời, đăng ký từ lời mời và quyền sở hữu.

Kiểm tra: token hết hạn/thu hồi/dùng lại bị từ chối; thành viên không xem hoặc
thay đổi dữ liệu quản lý của người khác; request từ website tải lên không thể
gọi API quản trị để thay đổi dữ liệu.

### Mốc 2 — Upload và chia sẻ

Xây ba hình thức upload, kiểm tra file, tạo slug và phục vụ theo hostname.
Giao diện responsive có sáng/tối, trạng thái upload và lỗi có hướng xử lý.

Kiểm tra: HTML đơn, ZIP và thư mục có CSS/JS/ảnh hoạt động; tài nguyên dùng
đường dẫn tuyệt đối hoạt động; hostname sai trả 404; kiểm tra traversal,
symlink, ZIP vượt hạn mức và file trùng đường dẫn đều bị chặn.

### Mốc 3 — Quản lý vòng đời

Xây đổi tên hiển thị, cập nhật giữ slug, xóa và hiển thị dung lượng.

Kiểm tra: cập nhật hợp lệ đổi nội dung cùng link; cập nhật lỗi giữ bản cũ;
không thể vượt quota bằng nhiều request; thiếu dung lượng được báo trước khi
ảnh hưởng bản đang chạy; restart phục hồi trạng thái và dọn dữ liệu thừa.

### Mốc 4 — Chạy thử trên router

1. Xác nhận kiến trúc, phân vùng dữ liệu và tài nguyên thực tế.
2. Cài binary vào vị trí riêng; tạo dịch vụ procd, quyền file và cấu hình.
3. Dùng listener riêng, ví dụ cổng 8088 nếu còn trống. Không thay cổng LuCI.
4. Kiểm tra local rồi mới bổ sung DNS/route Tunnel; không ghi đè dịch vụ hiện có.
5. Khởi động lại riêng dịch vụ GửiWeb và xác nhận dữ liệu còn nguyên.
6. Kiểm tra HTTPS từ bên ngoài với tài nguyên tĩnh và upload hợp lệ 50 MiB;
   giới hạn upload của Cloudflare/Tunnel cũng phải cho phép request multipart đó.

Hoàn thành khi binary thực sự chạy trên router; public link đúng website;
quản lý, ba kiểu upload, cập nhật và xóa đều hoạt động; RAM/CPU/disk được đo
trong lúc chạy cùng tunnel và chức năng mạng vẫn ổn. Chưa cam kết lượng truy
cập đồng thời khi chưa có số đo. Ghi lại kết quả và chọn giới hạn vận hành.

### Mốc 5 — Mở cho nhóm nhỏ

Mời 2–3 người dùng thử, dùng các website nhỏ trước, kiểm tra lỗi và dung lượng.
Sau đó mới tăng số thành viên. Không cần triển khai sao lưu tự động ở bản đầu,
nhưng tài liệu phải nói rõ dữ liệu có thể mất khi hỏng bộ nhớ/cài lại router.

## 7. Thông tin cần thu thập trước khi cài lên router

Không cần mật khẩu hoặc token trong chat. Chỉ cần kết quả kỹ thuật không nhạy cảm:

```sh
uname -m
ubus call system board
df -h
free -m
command -v cloudflared
```

Ngoài ra cần xác định:

- Tên miền và subdomain quản lý muốn dùng.
- Các subdomain đã dành cho dịch vụ khác.
- Tunnel chạy trực tiếp trên router hay trên máy khác; được quản lý bằng
  dashboard Cloudflare hay config local. Chỉ mô tả cách quản lý, không gửi token.
- Mount bền vững dùng lưu dữ liệu và quyền ghi của tài khoản dịch vụ.
- Cổng listener còn trống; tunnel có thể truy cập listener bằng địa chỉ nào.

## 8. Phát hành và quay lại phiên bản trước

- Build trong môi trường phát triển; chuyển binary kèm SHA-256 và ghi phiên bản.
- Thử bản mới trước khi thay binary đang chạy; giữ binary và cấu hình cũ.
- Dừng/khởi động lại riêng GửiWeb, xác nhận readiness và một website thực tế.
- Trước migration DB khi nâng cấp, tạo bản sao nhất quán bằng cơ chế SQLite
  phù hợp; không copy riêng file DB đang dùng WAL. Đây là bước nâng cấp, không
  phải hệ thống sao lưu tự động.
- Nếu lỗi, quay về binary/cấu hình và DB tương thích; nếu cần, chỉ thu hồi các
  route mới thêm cho GửiWeb. Không tác động route của dịch vụ khác.

## 9. Hiện trạng đã xác nhận trong cloud

- Checkout `/workspace/chia_se_web`, commit `8a07eae42d5570648170bd03ffd05010784d2291`.
- Trước tài liệu này repository chỉ có README; chưa có ứng dụng hoặc bộ test.
- Máy phát triển là x86_64, có Node.js 24.19.0 và Python 3.12.14.
- Lệnh `go version` trả `Go: Unknown option: version`; chưa xác nhận có Go SDK
  sử dụng được. Mốc 0 cần cài/kiểm tra Go chính thức.
- Chưa truy cập router, kiểm tra Tunnel hoặc đo hiệu năng thiết bị.
- Chưa lưu script môi trường vì chưa có lệnh cài/chạy ứng dụng được kiểm chứng.

## 10. Kết quả thực hiện và điều chỉnh

Đã xây backend Go/SQLite, giao diện tiếng Việt sáng/tối, tài khoản/lời mời,
ba kiểu upload, subdomain public, đổi tên, xóa và cập nhật giữ nguyên link.
Có test phân quyền, Origin/CSRF, quota, ZIP/path/symlink, restart và đồng thời.
Chi tiết kiểm chứng nằm trong `KIEM_CHUNG.md`.

Chủ router đã xác nhận `aarch64`, cloudflared cùng router và cổng 8088 chưa
có listener. Phân vùng `/overlay` hiện khoảng 1,9 GiB, trống khoảng 1,8 GiB;
RAM khả dụng khoảng 133 MiB. Có phân vùng `mmcblk0p27` khoảng 4,3 GiB nhưng
`block info` chưa nhận diện filesystem, nên chưa dùng để lưu website.

Cấu hình hiện hành giữ upload 50 MiB, website 100 MiB, giảm hạn mức tổng còn
512 MiB, thành viên 128 MiB và giữ trống 512 MiB. Dữ liệu đề xuất ở
`/overlay/guiweb`; dịch vụ Go có `GOMEMLIMIT=64MiB` (mục tiêu mềm). Chưa cần
format hoặc sửa phân vùng. Chỉ tăng hạn mức sau khi xác minh thêm lưu trữ.

Tên miền đã xác nhận là `phatnt.id.vn`; trang quản lý dùng `app.phatnt.id.vn`.
`in4.phatnt.id.vn` đang chạy dịch vụ khác nên `in4` nằm trong danh sách dành
riêng, route cũ phải giữ nguyên. Mốc cài đặt/public trên router và thử với
nhóm người dùng còn chờ cấu hình Tunnel thực tế, chuyển artifact và kiểm
chứng trên thiết bị.
