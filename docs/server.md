# Go 서버 실행 (골격)

설계: `docs/superpowers/specs/2026-11-09-go-server-design.md`, `...-api-schema.md`.

## 지금 되는 것
- 정적 대시보드 서빙(`prototype/`), Google 로그인 + 멤버 확인, 소유자 Google 연결(`/auth/owner`)
- `POST /api/bootstrap` (설정 탭 읽기·검증 + `me` + 지출), `refresh`, `addExpense` / `updateExpense` / `deleteExpense`
  - 지출은 소유자 시트의 지출 탭에 저장(탭이 없으면 자동 생성), 기존 10컬럼 + 기록자·수정자·수정시각
  - 수정·삭제는 `baseUpdatedAt`(보고 있던 수정시각)을 보내면 충돌을 409 로 막는다(생략하면 마지막 쓰기 우선)
  - 모든 쓰기는 `변경이력` 탭에 남는다(삭제 전 값 포함, 실패해도 저장 자체는 성공 처리)
- 권한(보기/편집자/소유자), CSRF 방어
- 아직 없는 것: 동선·캘린더·지도·사진·방문 API, SQLite, 화면의 서버 연결(`remote.js`는 아직 Apps Script용)
  → 서버가 서빙하는 화면은 **샘플 데이터 프로토타입**이다.

## 로컬 개발 (Google 없이)
```bash
DEV_LOGIN=1 OWNER_EMAIL=dev-owner@example.com SESSION_SECRET=$(openssl rand -hex 32) \
FIXTURE_DIR=dev/fixtures STATIC_DIR=prototype go run ./cmd/server
# 브라우저: http://localhost:8080/auth/dev?email=dev-owner@example.com
```
멤버 예시는 `dev/fixtures/멤버.json` (소유자/편집자/보기).

## Google 연결 (실서비스)
환경변수: `BASE_URL`, `OWNER_EMAIL`, `SESSION_SECRET`(32자 이상), `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `TRIP_SHEET_ID`, `DATA_DIR`.
OAuth 클라이언트(웹)의 리디렉션 URI 두 개를 등록: `<BASE_URL>/auth/callback`, `<BASE_URL>/auth/owner/callback`.
소유자로 로그인한 뒤 `<BASE_URL>/auth/owner` 를 열어 한 번 동의하면 refresh token 이 `DATA_DIR/owner_token.json`(0600)에 저장된다(임시 저장 방식, 암호화 안 됨).

## 테스트 / Docker
```bash
go test ./...
docker build -t tour-manager . && docker run --rm -p 8080:8080 -v tm-data:/data --env-file .env tour-manager
```
