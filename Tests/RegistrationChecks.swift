import Foundation

// Run with scripts/test-registration.sh. No TV or credentials are needed.
@main
struct RegistrationChecks {
    static func main() {
        let client = WebOSClient(host: "192.0.2.10", clientKey: nil)
        let legacyPayload = client.registrationPayload(forcePairing: true)
        let legacy = legacyPayload["manifest"] as! [String: Any]
        let signed = legacy["signed"] as! [String: Any]
        precondition(legacy["signatures"] != nil)
        precondition(legacyPayload["forcePairing"] as? Bool == true)
        precondition(legacyPayload["pairingType"] as? String == "PROMPT")

        let payload = client.registrationPayload(forcePairing: false, unsigned: true)
        let manifest = payload["manifest"] as! [String: Any]
        precondition(manifest["signed"] == nil && manifest["signatures"] == nil)
        let expected = Set((legacy["permissions"] as! [String]) + (signed["permissions"] as! [String]))
        precondition(Set(manifest["permissions"] as! [String]) == expected)
        precondition(expected.contains("CONTROL_INPUT_TEXT"))
        precondition(expected.contains("CONTROL_MOUSE_AND_KEYBOARD"))
        precondition(payload["forcePairing"] as? Bool == false)

        let error = "403 Pairing rejected: blacklisted certificate detected"
        precondition(WebOSClient.shouldRetryUnsigned(error: error, unsigned: false))
        precondition(!WebOSClient.shouldRetryUnsigned(error: error, unsigned: true))
        for message in ["403 User rejected pairing", "401 insufficient permissions", "Connection timed out"] {
            precondition(!WebOSClient.shouldRetryUnsigned(error: message, unsigned: false))
        }
        print("PASS: legacy manifest, unsigned permissions, pairing mode, bounded fallback, unrelated errors")
    }
}
