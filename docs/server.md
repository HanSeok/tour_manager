# Go 서버 실행 (골격)

설계: `docs/superpowers/specs/2026-11-09-go-server-design.md`, `...-api-schema.md`.

## 지금 되는 것
- 정적 대시보드 서빙(`prototype/`), Google 로그인 + 멤버 확인, 소유자 Google 연결(`/auth/owner`)
- `POST /api/bootstrap` (설정 탭 읽기·검증 + `me` + 지출), `refresh`, `addExpense` / `updateExpense` / `deleteExpense`
  - 지출은 소유자 시트의 지출 탭에 저장(탭이 없으면 자동 생성), 기존 10컬럼 + 기록자·수정자·수정시각
  - 수정·삭제는 `baseUpdatedAt`(보고 있던 수정시각)을 보내면 충돌을 409 로 막는다(생략하면 마지막 쓰기 우선)
  - 모든 쓰기는 `변경이력` 탭에 남는다(삭제 전 값 포함, 실패해도 저장 자체는 성공 처리)
- 권한(보기/편집자/소유자), CSRF 방어
- 화면은 `/servermode.js`(`TM_API = true`)로 서버 모드를 켜고 `/api`를 호출한다(`remote.js`).
  지출은 서버에 저장되고 다른 멤버 화면에 반영된다(새로고침 3분 주기 + 탭 복귀 시). 로그인한 사람·권한·작성자 표시,
  동시 수정 충돌(409) 안내, 오프라인 대기열(재전송 시 중복 없음)이 동작한다.
- 아직 서버로 안 가는 것: 동선·일정·장소·방문 기록(이 브라우저에만 저장), 사진, SQLite.
  개발 모드(FIXTURE_DIR)에서는 샘플 일정·장소를 보여주고, 실제 시트에 연결하면 일정·장소는 비어 있다.
  → 서버가 서빙하는 화면은 **샘플 데이터 프로토타입**이다.

## 로컬 개발 (Google 없이)
```bash
DEV_LOGIN=1 OWNER_EMAIL=dev-owner@example.com SESSION_SECRET=$(openssl rand -hex 32) \
FIXTURE_DIR=dev/fixtures STATIC_DIR=prototype go run ./cmd/server
# 브라우저: http://localhost:8080/auth/dev?email=dev-owner@example.com
```
멤버 예시는 `dev/fixtures/멤버.json` (소유자/편집자/보기).
여러 멤버를 동시에 보려면 같은 브라우저의 일반 창과 시크릿 창(또는 다른 브라우저)에서 각각 다른 이메일로 로그인한다(쿠키가 같으면 한 사람으로 보인다).
개발 서버의 시트는 메모리라서 재시작하면 지출이 사라진다.
브라우저 시나리오: `python3 prototype/e2e_api.py http://localhost:8080/` (새로 띄운 개발 서버 필요).

## Google 연결 (실서비스)
자세한 순서와 문제 해결 표: [`docs/google-setup.md`](google-setup.md). `.env.example` 를 `.env` 로 복사해 채운다(서버가 읽는다).
환경변수: `BASE_URL`, `OWNER_EMAIL`, `SESSION_SECRET`(32자 이상), `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `TRIP_SHEET_ID`, `DATA_DIR`.
OAuth 클라이언트(웹)의 리디렉션 URI 두 개를 등록: `<BASE_URL>/auth/callback`, `<BASE_URL>/auth/owner/callback`.
소유자로 로그인한 뒤 `<BASE_URL>/auth/owner` 를 열어 한 번 동의하면 refresh token 이 `DATA_DIR/owner_token.json`(0600)에 저장된다(임시 저장 방식, 암호화 안 됨).

## 테스트 / Docker
```bash
go test ./...
docker build -t tour-manager . && docker run --rm -p 8080:8080 -v tm-data:/data --env-file .env tour-manager
```
