// Package httpx 涓€?HTTP 鍩虹浠讹細涓氬姟閿欒鐮佷笌缁熶竴鍝嶅簲锛圓PI 璁捐 搂1/搂2锛夈€?// handler 涓?middleware 鍏卞悓寮曠敤锛岄伩鍏嶇浉浜掍緷璧栥€?package httpx

import "net/http"

// 涓氬姟閿欒鐮侊紝涓?md/API璁捐.md 搂2 淇濇寔涓€涓€瀵瑰簲锛屾柊澧炵爜鍏堟敼鏂囨。鍐嶆敼杩欓噷銆?const (
	CodeAuthInvalid    = "AUTH_001" // 401 鐢ㄦ埛鍚嶆垨瀵嗙爜閿欒
	CodeAuthLocked     = "AUTH_002" // 401 璐﹀彿宸查攣瀹?	CodeAuthDisabled   = "AUTH_003" // 401 璐﹀彿琚鐢?	CodeAuthStaleToken = "AUTH_004" // 401 token 澶辨晥锛堢増鏈彿涓嶇锛?	CodeRegisterClosed = "AUTH_005" // 403 鑷姪娉ㄥ唽鏈紑鏀?	CodePasswordWeak   = "AUTH_006" // 422 瀵嗙爜涓嶇鍚堣鍒欙紙鈮?8 浣嶏級
	// CodeAuthMustChangePassword 棣栫櫥寮哄埗鏀瑰瘑鏈畬鎴愶細闄ゆ敼瀵嗙櫧鍚嶅崟澶栦竴寰?403锛堝璁?P1-4锛夈€?	CodeAuthMustChangePassword = "AUTH_007" // 403 闇€鍏堜慨鏀瑰垵濮嬪瘑鐮?	CodeNotFound               = "NOT_FOUND"         // 404 璧勬簮涓嶅瓨鍦?	CodeValidation             = "VALIDATION_FAILED" // 422 鍙傛暟鏍￠獙澶辫触
	CodeUserExists             = "USER_001"          // 409 鐢ㄦ埛鍚嶅凡瀛樺湪
	CodeUserSelfDisable        = "USER_002"          // 422 涓嶈兘绂佺敤鑷繁
	CodeUserLastAdmin          = "USER_003"          // 422 鏈€鍚庝竴涓鐞嗗憳淇濇姢
	CodeKBNameExists           = "KB_001"            // 409 鐭ヨ瘑搴撳悕绉伴噸澶?	CodeDocRepoTooLarge        = "DOC_001"           // 422 浠撳簱瓒呰繃澶у皬/鏂囦欢鏁伴檺鍒?	CodeDocFormatNoSupport     = "DOC_002"           // 422 鏂囦欢鏍煎紡涓嶆敮鎸?	CodeDocCredInvalid         = "DOC_003"           // 422 浠撳簱鍑瘉鏃犳晥鎴栧湴鍧€涓嶅彲杈?	CodeDocSyncFailed          = "DOC_004"           // 422 鍚屾澶辫触锛堣繙绔笉鍙揪锛?	CodeDocTaskRunning         = "DOC_TASK_RUNNING"  // 409 宸叉湁杩愯涓换鍔?	CodeModelNoDefault         = "MODEL_001"         // 422 璇ョ敤閫旂己灏戦粯璁ゆ潯鐩?	CodeModelDefaultLocked     = "MODEL_002"         // 422 榛樿鏉＄洰涓嶅彲鍒犻櫎
	CodeModelHasEntries        = "MODEL_003"         // 409 妯″瀷鏈嶅姟涓嬪瓨鍦ㄦ潯鐩紝涓嶅彲鍒犻櫎
	// CodeModelHasDefaultEntry 鍋滅敤鎸佹湁榛樿鏉＄洰鐨勬湇鍔★紙瀹¤ P1-6锛夈€?	CodeModelHasDefaultEntry = "MODEL_004"    // 422 鏈嶅姟涓嬪瓨鍦ㄩ粯璁ゆ潯鐩紝涓嶅彲鍋滅敤
	CodeLLMUnavailable       = "LLM_001"      // 502 妯″瀷鏈嶅姟涓嶅彲鐢?	CodeLLMBadResponse       = "LLM_002"      // 502 妯″瀷鏈嶅姟鏃犳湁鏁堝搷搴?	CodeRateLimited          = "RATE_LIMITED" // 429 璇锋眰杩囦簬棰戠箒
	CodeDBUnreachable        = "SYS_001"      // 503 鏁版嵁搴撲笉鍙敤
	CodeRedisUnreachable     = "SYS_002"      // 503 Redis 涓嶅彲鐢?	CodeForbidden            = "FORBIDDEN"    // 403 閫氱敤瓒婃潈
)

var httpStatusByCode = map[string]int{
	CodeAuthInvalid:            http.StatusUnauthorized,
	CodeAuthLocked:             http.StatusUnauthorized,
	CodeAuthDisabled:           http.StatusUnauthorized,
	CodeAuthStaleToken:         http.StatusUnauthorized,
	CodeRegisterClosed:         http.StatusForbidden,
	CodePasswordWeak:           http.StatusUnprocessableEntity,
	CodeAuthMustChangePassword: http.StatusForbidden,
	CodeNotFound:               http.StatusNotFound,
	CodeValidation:             http.StatusUnprocessableEntity,
	CodeUserExists:             http.StatusConflict,
	CodeUserSelfDisable:        http.StatusUnprocessableEntity,
	CodeUserLastAdmin:          http.StatusUnprocessableEntity,
	CodeKBNameExists:           http.StatusConflict,
	CodeDocRepoTooLarge:        http.StatusUnprocessableEntity,
	CodeDocFormatNoSupport:     http.StatusUnprocessableEntity,
	CodeDocCredInvalid:         http.StatusUnprocessableEntity,
	CodeDocSyncFailed:          http.StatusUnprocessableEntity,
	CodeDocTaskRunning:         http.StatusConflict,
	CodeModelNoDefault:         http.StatusUnprocessableEntity,
	CodeModelDefaultLocked:     http.StatusUnprocessableEntity,
	CodeModelHasEntries:        http.StatusConflict,
	CodeModelHasDefaultEntry:   http.StatusUnprocessableEntity,
	CodeLLMUnavailable:         http.StatusBadGateway,
	CodeLLMBadResponse:         http.StatusBadGateway,
	CodeRateLimited:            http.StatusTooManyRequests,
	CodeDBUnreachable:          http.StatusServiceUnavailable,
	CodeRedisUnreachable:       http.StatusServiceUnavailable,
	CodeForbidden:              http.StatusForbidden,
}

// StatusOf 涓氬姟鐮佸搴?HTTP 鐘舵€佺爜锛涙湭鐭ョ爜鎸?500銆?func StatusOf(code string) int {
	if s, ok := httpStatusByCode[code]; ok {
		return s
	}
	return http.StatusInternalServerError
}
