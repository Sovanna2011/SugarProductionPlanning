sap.ui.define([
	"kss/spp/controller/BaseController",
	"kss/spp/model/formatter"
], function (BaseController, formatter) {
	"use strict";

	/**
	 * The daily plan against what was actually posted (requirement section 17).
	 *
	 * Actual figures are derived from posted movements, so this screen is a
	 * pure read: there is nothing here to key in.
	 */
	return BaseController.extend("kss.spp.controller.PlanVsActual", {

		formatter: formatter,

		onInit: function () {
			this._model = this.getAppModel();
			if (!this._model.getProperty("/planVsActual")) {
				this._model.setProperty("/planVsActual", { date: null, rows: [] });
			}
			this.getRouter().getRoute("planVsActual").attachPatternMatched(this._onRouteMatched, this);
		},

		_onRouteMatched: function () {
			// Default to the first planned day of the season rather than
			// today, which for a season plan is usually outside its range.
			if (!this._model.getProperty("/planVsActual/date")) {
				this._model.setProperty("/planVsActual/date", "2027-01-15");
			}
			this._load();
		},

		_load: function () {
			var that = this;
			var sDate = this._model.getProperty("/planVsActual/date");
			var sFactory = this._model.getProperty("/filters/factoryId");

			var oParams = new URLSearchParams();
			if (sFactory) { oParams.set("factoryId", sFactory); }
			if (sDate) { oParams.set("date", sDate); }

			this._model.setProperty("/busy", true);
			return this.get("planning/plan-vs-actual?" + oParams.toString())
				.then(function (aRows) {
					that._model.setProperty("/planVsActual/rows", aRows || []);
				})
				.catch(function (err) {
					that._model.setProperty("/planVsActual/rows", []);
					that.showError(err);
				})
				.finally(function () {
					that._model.setProperty("/busy", false);
				});
		},

		onDateChange: function () {
			this._load();
		},

		onRefresh: function () {
			this._load();
		},

		onNavBack: function () {
			this.getRouter().navTo("dashboard");
		}
	});
});
