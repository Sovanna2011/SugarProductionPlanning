sap.ui.define([
	"sap/ui/core/mvc/Controller",
	"sap/ui/model/json/JSONModel",
	"sap/m/MessageToast",
	"sap/ui/core/Fragment",
	"kss/spp/model/formatter"
], function (Controller, JSONModel, MessageToast, Fragment, formatter) {
	"use strict";

	return Controller.extend("kss.spp.controller.Dashboard", {

		formatter: formatter,

		onInit: function () {
			this._model = this.getOwnerComponent().getModel("app");
			this._api = this.getOwnerComponent().getApiBase();
			this._loadMasterData().then(this._loadDashboard.bind(this));
		},

		// --- data loading ----------------------------------------------------

		/**
		 * GETs a JSON endpoint, throwing on a non-2xx so one failed call
		 * surfaces as a message strip rather than an empty dashboard.
		 */
		_get: function (sPath) {
			return fetch(this._api + "/api/v1/" + sPath, {
				headers: { "Accept": "application/json" }
			}).then(function (res) {
				if (!res.ok) {
					return res.json()
						.catch(function () { return { error: res.statusText }; })
						.then(function (body) {
							throw new Error(body.error || ("HTTP " + res.status));
						});
				}
				return res.json();
			});
		},

		_loadMasterData: function () {
			var that = this;
			this._model.setProperty("/busy", true);

			return Promise.all([
				this._get("factories"),
				this._get("master/storage-types"),
				this._get("master/storage-locations"),
				this._get("master/products"),
				this._get("master/packaging-types")
			]).then(function (aResults) {
				// A leading blank entry gives each filter an "all" option.
				that._model.setProperty("/factories", aResults[0]);
				that._model.setProperty("/storageTypes",
					[{ code: "", name: "(All)" }].concat(aResults[1]));
				that._model.setProperty("/storageLocations",
					[{ storageCode: "", storageName: "(All)" }].concat(aResults[2]));
				that._model.setProperty("/products",
					[{ code: "", name: "(All)" }].concat(aResults[3]));
				that._model.setProperty("/packagingTypes",
					[{ code: "", description: "(All)" }].concat(aResults[4]));

				if (aResults[0].length && !that._model.getProperty("/filters/factoryId")) {
					that._model.setProperty("/filters/factoryId", String(aResults[0][0].id));
				}
			}).catch(function (err) {
				that._model.setProperty("/error", "Could not load master data: " + err.message);
			}).finally(function () {
				that._model.setProperty("/busy", false);
			});
		},

		_loadDashboard: function () {
			var that = this;
			var oFilters = this._model.getProperty("/filters");
			var oParams = new URLSearchParams();

			if (oFilters.factoryId) { oParams.set("factoryId", oFilters.factoryId); }
			if (oFilters.date) { oParams.set("date", oFilters.date); }
			if (oFilters.storageType) { oParams.set("storageType", oFilters.storageType); }
			if (oFilters.storageCode) { oParams.set("storageCode", oFilters.storageCode); }
			if (oFilters.productCode) { oParams.set("productCode", oFilters.productCode); }
			if (oFilters.packagingCode) { oParams.set("packagingCode", oFilters.packagingCode); }

			this._model.setProperty("/busy", true);
			this._model.setProperty("/error", "");

			return this._get("dashboard/storage-capacity?" + oParams.toString())
				.then(function (oData) {
					that._model.setProperty("/dashboard", oData);
					that._model.setProperty("/loaded", true);
				})
				.catch(function (err) {
					that._model.setProperty("/error", "Could not load the dashboard: " + err.message);
				})
				.finally(function () {
					that._model.setProperty("/busy", false);
				});
		},

		// --- filter handling -------------------------------------------------

		onFilterChange: function () {
			this._loadDashboard();
		},

		onRefresh: function () {
			this._loadDashboard();
		},

		onClearFilters: function () {
			var oFilters = this._model.getProperty("/filters");
			this._model.setProperty("/filters", {
				factoryId: oFilters.factoryId,
				date: null,
				storageType: "",
				storageCode: "",
				productCode: "",
				packagingCode: ""
			});
			this._loadDashboard();
		},

		// --- forward projection (requirement section 19) ----------------------

		/**
		 * Opens the forward capacity curve for the pressed storage card. The
		 * scope is a location code; pooled groups are projected the same way.
		 */
		onProject: function (oEvent) {
			var oContext = oEvent.getSource().getBindingContext("app");
			var sScope = oContext.getProperty("storageCode") || oContext.getProperty("groupCode");
			var sName = oContext.getProperty("storageName") || oContext.getProperty("groupName");
			var iFactory = this._model.getProperty("/filters/factoryId");
			var that = this;

			var oParams = new URLSearchParams({ scope: sScope, useActualOpening: "true" });
			if (iFactory) { oParams.set("factoryId", iFactory); }

			this._model.setProperty("/busy", true);
			this._get("planning/projection?" + oParams.toString())
				.then(function (oProjection) {
					that._openProjectionDialog(sName, oProjection);
				})
				.catch(function (err) {
					MessageToast.show("No projection available for " + sName + ": " + err.message);
				})
				.finally(function () {
					that._model.setProperty("/busy", false);
				});
		},

		_openProjectionDialog: function (sName, oProjection) {
			var that = this;

			// Only the first 60 days go into the dialog; a full season is 276
			// rows and unreadable in a popup. The horizons summarise the rest.
			oProjection.previewDays = (oProjection.days || []).slice(0, 60);
			oProjection.truncated = (oProjection.days || []).length > 60;

			var pDialog = this._pProjectionDialog || (this._pProjectionDialog = Fragment.load({
				id: this.getView().getId(),
				name: "kss.spp.view.ProjectionDialog",
				controller: this
			}).then(function (oDialog) {
				that.getView().addDependent(oDialog);
				return oDialog;
			}));

			return pDialog.then(function (oDialog) {
				oDialog.setModel(new JSONModel(oProjection), "proj");
				oDialog.setTitle("Projected capacity — " + sName);
				oDialog.open();
			});
		},

		onCloseProjection: function () {
			if (this._oProjectionDialog) {
				this._oProjectionDialog.close();
			}
		}
	});
});
