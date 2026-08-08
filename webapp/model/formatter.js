sap.ui.define([], function () {
	"use strict";

	function isNumber(v) {
		return typeof v === "number" && isFinite(v);
	}

	function group(value, decimals) {
		return value.toLocaleString("en-US", {
			minimumFractionDigits: decimals,
			maximumFractionDigits: decimals
		});
	}

	// Named so the helpers below can call each other. UI5 invokes a formatter
	// with `this` bound to the controller, not to this module, so `this.qty`
	// would not resolve.
	var formatter = {

		/**
		 * A percentage for display, e.g. "82.5%".
		 */
		percent: function (value) {
			if (!isNumber(value)) {
				return "";
			}
			return group(value, 2) + "%";
		},

		/**
		 * ProgressIndicator only accepts 0..100. A location can be over its
		 * physical capacity (an override was posted), so the bar clamps while
		 * the display value keeps the true figure.
		 */
		barPercent: function (value) {
			if (!isNumber(value)) {
				return 0;
			}
			return Math.max(0, Math.min(100, value));
		},

		/**
		 * A quantity with its unit, e.g. "35,000 TON".
		 */
		qty: function (value, uom) {
			if (!isNumber(value)) {
				return "";
			}
			return group(value, value % 1 === 0 ? 0 : 3) + (uom ? " " + uom : "");
		},

		/**
		 * A weight in tons.
		 */
		tons: function (value) {
			if (!isNumber(value)) {
				return "—";
			}
			return group(value, value % 1 === 0 ? 0 : 3) + " t";
		},

		/**
		 * A package count. Bulk lines carry no package count, shown as a dash.
		 */
		packages: function (value) {
			if (!isNumber(value) || value === 0) {
				return "—";
			}
			return group(value, 0);
		},

		/**
		 * Renders whichever ceiling is configured. A location that counts
		 * packages shows the bag limit; a tank or silo shows the weight limit.
		 */
		maximum: function (packages, weight) {
			if (isNumber(packages)) {
				return group(packages, 0) + " pkg";
			}
			if (isNumber(weight)) {
				return group(weight, weight % 1 === 0 ? 0 : 3) + " t";
			}
			return "—";
		},

		/**
		 * The headline the requirement asks to be highlighted: the first date
		 * the scope is expected to overflow.
		 */
		breachText: function (date, capacity, uom) {
			if (!date) {
				return "";
			}
			return "Expected to exceed the physical capacity of " +
				formatter.qty(capacity, uom) + " on " + formatter.shortDate(date) + ".";
		},

		safeBreachText: function (date, safeCapacity, uom) {
			if (!date) {
				return "";
			}
			return "Expected to exceed the safe capacity of " +
				formatter.qty(safeCapacity, uom) + " on " + formatter.shortDate(date) + ".";
		},

		/**
		 * A yyyy-MM-dd date from the API, shown as "28 May 2027".
		 */
		shortDate: function (value) {
			if (!value) {
				return "—";
			}
			var d = new Date(value);
			if (isNaN(d.getTime())) {
				return value;
			}
			return d.toLocaleDateString("en-GB", {
				day: "2-digit", month: "short", year: "numeric"
			});
		}
	};

	return formatter;
});
