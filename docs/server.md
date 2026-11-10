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
환경변수: `BASE_URL`, `OWNER_EMAIL`, `SESSION_SECRET`(32자 이상), `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `TRIP_SHEET_ID`(선택: 비우면 대시보드에서 여행을 만듦, 목록은 `DATA_DIR/trips.json`), `DATA_DIR`.
OAuth 클라이언트(웹)의 리디렉션 URI 두 개를 등록: `<BASE_URL>/auth/callback`, `<BASE_URL>/auth/owner/callback`.
소유자로 로그인한 뒤 `<BASE_URL>/auth/owner` 를 열어 한 번 동의하면 refresh token 이 `DATA_DIR/owner_token.json`(0600)에 저장된다(임시 저장 방식, 암호화 안 됨).

## 테스트 / Docker
```bash
go test ./...
docker build -t tour-manager . && docker run --rm -p 8080:8080 -v tm-data:/data --env-file .env tour-manager
```

## 장소 추가 (Go 서버)
편집자·소유자는 대시보드 지도를 눌러 가고 싶은 곳을 추가합니다(이름, 분류 5종, 메모). 장소는 시트의 `장소` 탭
(ID | 이름 | 분류 | 위도 | 경도 | 메모 | 기록자 | 수정자 | 수정시각 | 링크)에 저장되어 모든 멤버와 공유되고, 지도 핀의 팝업에
작성자가 표시됩니다. 보기 권한은 볼 수만 있습니다. 삭제는 팝업의 **장소 삭제**(변경이력에 이전 값이 남음).
API: `addPlace(place)`, `updatePlace(id, place, baseUpdatedAt?)`, `deletePlace(id, baseUpdatedAt?)` — 응답은 전체 장소 목록이고
`bootstrap`/`refresh` 의 `tripPlaces` 로도 내려갑니다. 이름 검색(Places API)은 아직 없습니다.

### 구글 지도·카카오맵 링크로 추가
지도 왼쪽 위의 **🔗 링크로 추가** 에 구글 지도(앱·웹)에서 장소를 연 뒤 **공유**로 복사한 링크(`https://maps.app.goo.gl/…`) 또는 카카오맵 링크(`https://place.map.kakao.com/<번호>`, `kko.to/…`, `map.kakao.com/link/map/이름,위도,경도`)를
붙여 넣고 **찾기**를 누르면, 서버가 링크를 따라가 **장소 이름과 위치(핀 좌표)** 를 읽어 새 장소 폼에 채워 줍니다. 분류만 고르고 추가하면 됩니다.
공유 문구 전체를 붙여 넣어도 첫 링크를 찾아 씁니다. API 키·과금이 없고, 구글 지도 도메인만 열어 보며(리다이렉트 5회까지, https),
페이지 내용은 읽지 않고 이동 주소만 해석합니다. 좌표가 없는 일반 장소 주소(`google.com/maps/place/이름`)는 위치를 알 수 없어 거절됩니다.
API: `resolvePlaceLink(text)` → `{name, lat, lng}`.

카카오맵: 장소 페이지(`place.map.kakao.com/<번호>`)는 이름·도로명 주소(메모에 채움)·좌표를 읽고, 카카오의 분류(음식점·카페 → 식당, 숙박 → 숙소,
교통, 쇼핑, 관광 …)를 폼의 분류로 미리 골라 줍니다. 좌표는 카카오 장소 페이지가 내부적으로 쓰는 주소(`place-api.map.kakao.com`)에서
읽습니다. **공식 문서화된 API 가 아니라서 카카오가 바꾸면 이 경로만 동작하지 않을 수 있습니다**(그때는 "읽지 못했습니다" 안내, 다른 기능은 영향 없음).
`map.kakao.com/link/map/…` 형식은 링크 자체에 좌표가 있어 영향이 없습니다. 접속하는 곳은 구글·카카오 도메인(https)뿐입니다.

**원본 링크 보관**: 링크로 추가한 장소는 공유받은 링크(구글 짧은 링크, 카카오는 `place.map.kakao.com/<번호>`)가 `장소` 탭의 `링크` 열에 함께
저장됩니다. 핀 팝업의 "구글 지도에서 보기 / 카카오맵에서 보기", 동선 카드와 후보 목록의 **🔗** 를 누르면 원본 페이지(사진·후기)가 새 탭으로 열립니다.
저장·표시되는 링크는 구글·카카오 지도 주소(https)뿐이고 그 외 주소(`javascript:` 포함)는 서버가 거절합니다. `링크` 열이 없던 기존 탭은 처음 쓸 때 헤더가 보강됩니다.

## 여행 전환
소유자 화면 헤더의 **여행 바꾸기**로 서버가 만든 여행 중 하나를 고릅니다. 고른 여행은 서버 전체에 적용되어 동행 모두에게 보입니다.
브라우저에만 저장되는 동선 등은 여행별로 따로 보관되어, 여행을 바꿔도 섞이지 않고 다시 돌아오면 복원됩니다.
