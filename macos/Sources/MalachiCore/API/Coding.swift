// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Helpers that make the Go contract's JSON habits decode into plain Swift:
// a nil slice that encoding/json writes as `null`, and RFC 3339 dates with
// or without fractional seconds and with `Z` or a numeric offset.

import Foundation

/// A slice the daemon may send as `null` or leave out (Go's nil slice on a
/// field without `omitempty`), read as an empty array. Encodes as an array.
@propertyWrapper
public struct NullAsEmpty<Element: Codable & Sendable>: Codable, Sendable {
    public var wrappedValue: [Element]

    public init(wrappedValue: [Element]) {
        self.wrappedValue = wrappedValue
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.singleValueContainer()
        wrappedValue = c.decodeNil() ? [] : try c.decode([Element].self)
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.singleValueContainer()
        try c.encode(wrappedValue)
    }
}

extension NullAsEmpty: Equatable where Element: Equatable {}
extension NullAsEmpty: Hashable where Element: Hashable {}

extension KeyedDecodingContainer {
    /// A missing key reads as an empty array too: synthesized `Decodable`
    /// calls this overload for `@NullAsEmpty` properties.
    public func decode<E>(_ type: NullAsEmpty<E>.Type, forKey key: Key) throws -> NullAsEmpty<E> {
        try decodeIfPresent(type, forKey: key) ?? NullAsEmpty(wrappedValue: [])
    }
}

/// RFC 3339 timestamps as Go's `time.Time` writes them (`2026-09-02T10:00:00Z`,
/// with up to nine fractional digits, `Z` or `+hh:mm`), parsed without a
/// `Calendar` so that the result is the same on every locale and for year 1.
public enum RFC3339 {
    /// Parses one timestamp; nil when the string is not one.
    public static func parse(_ s: String) -> Date? {
        let b = Array(s.utf8)
        // 0001-01-01T00:00:00Z is the shortest form.
        guard b.count >= 20 else { return nil }
        guard let year = digits(b, 0, 4), b[4] == dash,
              let month = digits(b, 5, 2), b[7] == dash,
              let day = digits(b, 8, 2), b[10] == upperT || b[10] == lowerT,
              let hour = digits(b, 11, 2), b[13] == colon,
              let minute = digits(b, 14, 2), b[16] == colon,
              let second = digits(b, 17, 2) else { return nil }
        guard (1...12).contains(month), (1...31).contains(day), hour < 24, minute < 60, second <= 60 else {
            return nil
        }
        var i = 19
        var fraction = 0.0
        if i < b.count, b[i] == dot {
            i += 1
            var scale = 0.1
            var any = false
            while i < b.count, isDigit(b[i]) {
                fraction += Double(b[i] - zero) * scale
                scale /= 10
                i += 1
                any = true
            }
            guard any else { return nil }
        }
        guard i < b.count else { return nil }
        var offset = 0
        if b[i] == upperZ || b[i] == lowerZ {
            i += 1
        } else if b[i] == plus || b[i] == minus {
            let sign = b[i] == plus ? 1 : -1
            guard b.count >= i + 6, let oh = digits(b, i + 1, 2), b[i + 3] == colon, let om = digits(b, i + 4, 2),
                  oh < 24, om < 60 else { return nil }
            offset = sign * (oh * 3600 + om * 60)
            i += 6
        } else {
            return nil
        }
        guard i == b.count else { return nil }
        let days = daysFromCivil(year: year, month: month, day: day)
        let seconds = days * 86400 + hour * 3600 + minute * 60 + second - offset
        return Date(timeIntervalSince1970: Double(seconds) + fraction)
    }

    /// Formats an instant as Go's `time.Time` would (RFC 3339 in UTC, the
    /// fraction only when there is one, at most microseconds). Foundation's
    /// `.iso8601` is not used because it applies the Julian calendar before
    /// 1582 and writes Go's zero time as `0001-01-03`.
    public static func format(_ date: Date) -> String {
        let totalMicros = Int64((date.timeIntervalSince1970 * 1_000_000).rounded())
        let seconds = floorDiv(totalMicros, 1_000_000)
        let micros = Int(totalMicros - seconds * 1_000_000)
        let days = floorDiv(seconds, 86400)
        let secondOfDay = Int(seconds - days * 86400)
        let (year, month, day) = civilFromDays(Int(days))
        var s = pad(year, 4) + "-" + pad(month, 2) + "-" + pad(day, 2)
        s += "T" + pad(secondOfDay / 3600, 2) + ":" + pad(secondOfDay / 60 % 60, 2) + ":" + pad(secondOfDay % 60, 2)
        if micros != 0 {
            var frac = pad(micros, 6)
            while frac.hasSuffix("0") { frac.removeLast() }
            s += "." + frac
        }
        return s + "Z"
    }

    /// Days since 1970-01-01 of a proleptic Gregorian date (Howard Hinnant's
    /// days_from_civil), valid for every year Go can produce.
    static func daysFromCivil(year: Int, month: Int, day: Int) -> Int {
        let y = month <= 2 ? year - 1 : year
        let era = (y >= 0 ? y : y - 399) / 400
        let yoe = y - era * 400
        let doy = (153 * (month + (month > 2 ? -3 : 9)) + 2) / 5 + day - 1
        let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy
        return era * 146097 + doe - 719468
    }

    /// The inverse of `daysFromCivil` (civil_from_days).
    static func civilFromDays(_ days: Int) -> (year: Int, month: Int, day: Int) {
        let z = days + 719468
        let era = (z >= 0 ? z : z - 146096) / 146097
        let doe = z - era * 146097
        let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365
        let doy = doe - (365 * yoe + yoe / 4 - yoe / 100)
        let mp = (5 * doy + 2) / 153
        let day = doy - (153 * mp + 2) / 5 + 1
        let month = mp < 10 ? mp + 3 : mp - 9
        let year = yoe + era * 400 + (month <= 2 ? 1 : 0)
        return (year, month, day)
    }

    private static func floorDiv(_ a: Int64, _ b: Int64) -> Int64 {
        let q = a / b
        return (a % b != 0 && (a < 0) != (b < 0)) ? q - 1 : q
    }

    private static func pad(_ v: Int, _ width: Int) -> String {
        let s = String(v)
        return s.count >= width ? s : String(repeating: "0", count: width - s.count) + s
    }

    private static func digits(_ b: [UInt8], _ start: Int, _ count: Int) -> Int? {
        guard b.count >= start + count else { return nil }
        var v = 0
        for i in start..<(start + count) {
            guard isDigit(b[i]) else { return nil }
            v = v * 10 + Int(b[i] - zero)
        }
        return v
    }

    private static func isDigit(_ c: UInt8) -> Bool { c >= zero && c <= nine }

    private static let zero = UInt8(ascii: "0")
    private static let nine = UInt8(ascii: "9")
    private static let dash = UInt8(ascii: "-")
    private static let colon = UInt8(ascii: ":")
    private static let dot = UInt8(ascii: ".")
    private static let plus = UInt8(ascii: "+")
    private static let minus = UInt8(ascii: "-")
    private static let upperT = UInt8(ascii: "T")
    private static let lowerT = UInt8(ascii: "t")
    private static let upperZ = UInt8(ascii: "Z")
    private static let lowerZ = UInt8(ascii: "z")
}

extension Date {
    /// Go's zero `time.Time` (0001-01-01T00:00:00Z), which the daemon writes
    /// for a date it never set (`Draft.updatedAt` in a `draft.create` result).
    public static let goZero = Date(timeIntervalSince1970: Double(RFC3339.daysFromCivil(year: 1, month: 1, day: 1) * 86400))

    /// True for any instant in year 1: what Go's `time.Time.IsZero` says of
    /// the value once it crossed the wire (fractions are lost, not the year).
    public var isGoZero: Bool {
        let year1 = Date.goZero.timeIntervalSince1970
        let year2 = Double(RFC3339.daysFromCivil(year: 2, month: 1, day: 1) * 86400)
        return timeIntervalSince1970 >= year1 && timeIntervalSince1970 < year2
    }
}
