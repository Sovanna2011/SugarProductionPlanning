sap.ui.define([
	"kss/spp/controller/BaseController",
	"sap/ui/model/json/JSONModel",
	"sap/ui/core/Fragment",
	"sap/m/MessageToast",
	"kss/spp/model/formatter"
], function (BaseController, JSONModel, Fragment, MessageToast, formatter) {
	"use strict";

	// The alert bands the system falls back on, offered as a reset.
	var DEFAULT_BANDS = [
		{ code: "NORMAL", name: "Normal", severity: "NORMAL", uiState: "Success", fromPercentage: 0, toPercentage: 80 },
		{ code: "WARNING", name: "Warning", severity: "WARNING", uiState: "Warning", fromPercentage: 80, toPercentage: 90 },
		{ code: "HIGH", name: "High", severity: "HIGH", uiState: "Warning", fromPercentage: 90, toPercentage: 95 },
		{ code: "CRITICAL", name: "Critical", severity: "CRITICAL", uiState: "Error", fromPercentage: 95, toPercentage: 100 },
		{ code: "FULL", name: "Full", severity: "FULL", uiState: "Error", fromPercentage: 100, toPercentage: null }
	];

	return BaseController.extend("kss.spp.controller.MasterData", {

		formatter: formatter,

		onInit: function () {
			this._model = this.getAppModel();
			this.getRouter().getRoute("masterData").attachPatternMatched(this._onRouteMatched, this);
		},

		_onRouteMatched: function () {
			this._load();
		},

		_factoryId: function () {
			return this._model.getProperty("/filters/factoryId") || "";
		},

		/**
		 * Loads everything the four tabs show. Master data is small, so one
		 * round of requests on entry is simpler than lazy-loading per tab.
		 */
		_load: function () {
			var that = this;
			var sFactory = this._factoryId();
			var sQuery = sFactory ? "?factoryId=" + sFactory : "";

			this._model.setProperty("/busy", true);

			return Promise.all([
				this.get("master/storage-locations" + (sFactory ? sQuery + "&includeInactive=true" : "?includeInactive=true")),
				this.get("master/storage-capacities" + sQuery),
				this.get("master/packaging-types"),
				this.get("master/threshold-levels" + sQuery),
				this.get("master/storage-types"),
				this.get("master/products")
			]).then(function (a) {
				that._model.setProperty("/master/storageLocations", a[0]);
				that._model.setProperty("/master/capacities", a[1]);
				that._model.setProperty("/master/packagingTypes", a[2]);
				that._model.setProperty("/master/thresholds", a[3]);
				// The dialogs pick from these; refresh them here so the
				// screen works even when opened directly by URL.
				that._model.setProperty("/storageTypes", a[4]);
				that._model.setProperty("/products", a[5]);
				that._model.setProperty("/packagingTypes", a[2]);
			}).catch(function (err) {
				that.showError(err);
			}).finally(function () {
				that._model.setProperty("/busy", false);
			});
		},

		onRefresh: function () {
			this._load();
		},

		onNavBack: function () {
			this.getRouter().navTo("dashboard");
		},

		// --- dialog plumbing -------------------------------------------------

		/**
		 * Opens a fragment against an "edit" model holding a working copy, so
		 * cancelling leaves the table untouched.
		 */
		_openDialog: function (sName, oData) {
			var that = this;
			this._dialogs = this._dialogs || {};

			var pDialog = this._dialogs[sName] || (this._dialogs[sName] = Fragment.load({
				id: this.getView().getId() + "-" + sName,
				name: "kss.spp.view." + sName,
				controller: this
			}).then(function (oDialog) {
				that.getView().addDependent(oDialog);
				return oDialog;
			}));

			return pDialog.then(function (oDialog) {
				oDialog.setModel(new JSONModel(oData), "edit");
				that._currentDialog = oDialog;
				oDialog.open();
				return oDialog;
			});
		},

		onCloseDialog: function () {
			if (this._currentDialog) {
				this._currentDialog.close();
			}
		},

		/**
		 * Shared save tail: close, report warnings, reload.
		 */
		_afterSave: function (oResult, sToastKey) {
			this.onCloseDialog();
			if (!this.showWarnings(oResult && oResult.warnings)) {
				MessageToast.show(this.getText(sToastKey));
			}
			return this._load();
		},

		// --- storage locations -----------------------------------------------

		onNewLocation: function () {
			this._openDialog("LocationDialog", {
				title: this.getText("newLocation"),
				isUpdate: false,
				storageCode: "",
				storageName: "",
				storageTypeCode: "FINISHED_GOODS_WAREHOUSE",
				physicalCapacity: 0,
				capacityUom: "TON",
				minimumStockLevel: 0,
				safeCapacityPercentage: 95,
				allowMixedProducts: true,
				allowMixedBatches: true,
				status: "ACTIVE",
				remark: "",
				showForce: false,
				force: false
			});
		},

		onEditLocation: function (oEvent) {
			var o = oEvent.getSource().getBindingContext("app").getObject();
			this._openDialog("LocationDialog", {
				title: this.getText("editLocation", [o.storageCode]),
				isUpdate: true,
				version: o.version,
				storageCode: o.storageCode,
				storageName: o.storageName,
				storageTypeCode: o.storageTypeCode,
				physicalCapacity: o.physicalCapacity,
				capacityUom: o.capacityUom,
				minimumStockLevel: o.minimumStockLevel,
				safeCapacityPercentage: o.safeCapacityPercentage,
				allowMixedProducts: o.allowMixedProducts,
				allowMixedBatches: o.allowMixedBatches,
				status: o.status,
				remark: o.remark,
				showForce: false,
				force: false
			});
		},

		onSaveLocation: function () {
			var that = this;
			var oEdit = this._currentDialog.getModel("edit");
			var d = oEdit.getData();

			var oBody = {
				factoryId: Number(this._factoryId()) || 1,
				storageCode: d.storageCode,
				storageName: d.storageName,
				storageTypeCode: d.storageTypeCode,
				physicalCapacity: Number(d.physicalCapacity),
				capacityUom: d.capacityUom,
				minimumStockLevel: Number(d.minimumStockLevel) || 0,
				safeCapacityPercentage: Number(d.safeCapacityPercentage),
				allowMixedProducts: !!d.allowMixedProducts,
				allowMixedBatches: !!d.allowMixedBatches,
				status: d.status,
				remark: d.remark || "",
				force: !!d.force,
				updatedBy: "dashboard"
			};
			if (d.isUpdate) {
				oBody.version = d.version;
			}

			this.post("master/storage-locations", oBody)
				.then(function (oResult) {
					return that._afterSave(oResult, "locationSaved");
				})
				.catch(function (err) {
					// Shrinking below current stock is refused once; offering
					// the override here is what makes the refusal actionable.
					if (/more than the new capacity/.test(err.message || "")) {
						oEdit.setProperty("/showForce", true);
					}
					that.showError(err);
				});
		},

		// --- product capacity -------------------------------------------------

		onNewCapacity: function () {
			var aLocations = this._model.getProperty("/master/storageLocations") || [];
			var aProducts = this._model.getProperty("/products") || [];
			var aPackaging = this._model.getProperty("/packagingTypes") || [];

			this._openDialog("CapacityDialog", {
				title: this.getText("newCapacity"),
				isUpdate: false,
				storageCode: aLocations.length ? aLocations[0].storageCode : "",
				productCode: aProducts.length ? aProducts[0].code : "",
				packagingCode: aPackaging.length ? aPackaging[0].code : "",
				maximumPackageQuantity: null,
				maximumWeightQuantity: null,
				status: "ACTIVE",
				remark: ""
			});
		},

		onEditCapacity: function (oEvent) {
			var o = oEvent.getSource().getBindingContext("app").getObject();
			this._openDialog("CapacityDialog", {
				title: this.getText("editCapacity", [o.storageCode, o.productName]),
				isUpdate: true,
				version: o.version,
				storageCode: o.storageCode,
				productCode: o.productCode,
				packagingCode: o.packagingCode,
				maximumPackageQuantity: o.maximumPackageQuantity,
				maximumWeightQuantity: o.maximumWeightQuantity,
				status: o.status,
				remark: o.remark
			});
		},

		onSaveCapacity: function () {
			var that = this;
			var d = this._currentDialog.getModel("edit").getData();

			// A blank ceiling means "not constrained on this axis", which the
			// API expects as an omitted field rather than a zero.
			var oBody = {
				storageCode: d.storageCode,
				productCode: d.productCode,
				packagingCode: d.packagingCode,
				status: d.status,
				remark: d.remark || "",
				updatedBy: "dashboard"
			};
			if (d.maximumPackageQuantity !== null && d.maximumPackageQuantity !== "") {
				oBody.maximumPackageQuantity = Number(d.maximumPackageQuantity);
			}
			if (d.maximumWeightQuantity !== null && d.maximumWeightQuantity !== "") {
				oBody.maximumWeightQuantity = Number(d.maximumWeightQuantity);
			}
			if (d.isUpdate) {
				oBody.version = d.version;
			}

			this.post("master/storage-capacities", oBody)
				.then(function (oResult) {
					return that._afterSave(oResult, "capacitySaved");
				})
				.catch(function (err) { that.showError(err); });
		},

		// --- packaging master --------------------------------------------------

		onNewPackaging: function () {
			this._openDialog("PackagingDialog", {
				title: this.getText("newPackaging"),
				isUpdate: false,
				code: "",
				description: "",
				netWeight: 0,
				netWeightUom: "KG",
				isBulk: false,
				status: "ACTIVE"
			});
		},

		onEditPackaging: function (oEvent) {
			var o = oEvent.getSource().getBindingContext("app").getObject();
			this._openDialog("PackagingDialog", {
				title: this.getText("editPackaging", [o.code]),
				isUpdate: true,
				version: o.version,
				code: o.code,
				description: o.description,
				netWeight: o.netWeight,
				netWeightUom: o.netWeightUom,
				isBulk: o.isBulk,
				status: o.status
			});
		},

		/** Bulk packaging carries no per-package weight. */
		onBulkToggled: function (oEvent) {
			if (oEvent.getParameter("state")) {
				this._currentDialog.getModel("edit").setProperty("/netWeight", 0);
			}
		},

		onSavePackaging: function () {
			var that = this;
			var d = this._currentDialog.getModel("edit").getData();

			var oBody = {
				code: d.code,
				description: d.description,
				netWeight: Number(d.netWeight) || 0,
				netWeightUom: d.netWeightUom,
				isBulk: !!d.isBulk,
				status: d.status,
				updatedBy: "dashboard"
			};
			if (d.isUpdate) {
				oBody.version = d.version;
			}

			this.post("master/packaging-types", oBody)
				.then(function (oResult) {
					return that._afterSave(oResult, "packagingSaved");
				})
				.catch(function (err) { that.showError(err); });
		},

		// --- alert thresholds ---------------------------------------------------

		onAddBand: function () {
			var aBands = (this._model.getProperty("/master/thresholds") || []).slice();
			var last = aBands[aBands.length - 1];
			aBands.push({
				code: "",
				name: "",
				severity: "WARNING",
				uiState: "Warning",
				fromPercentage: last ? (last.toPercentage || last.fromPercentage) : 0,
				toPercentage: null
			});
			this._model.setProperty("/master/thresholds", aBands);
		},

		onRemoveBand: function (oEvent) {
			var oContext = oEvent.getSource().getBindingContext("app");
			var iIndex = Number(oContext.getPath().split("/").pop());
			var aBands = (this._model.getProperty("/master/thresholds") || []).slice();
			aBands.splice(iIndex, 1);
			this._model.setProperty("/master/thresholds", aBands);
		},

		onResetBands: function () {
			this._model.setProperty("/master/thresholds",
				JSON.parse(JSON.stringify(DEFAULT_BANDS)));
		},

		onSaveBands: function () {
			var that = this;
			var aBands = (this._model.getProperty("/master/thresholds") || []).map(function (b) {
				// A blank upper bound means "and above"; the API wants it
				// omitted rather than sent as an empty string.
				var oBand = {
					code: b.code,
					name: b.name,
					severity: b.severity,
					uiState: b.uiState || "None",
					fromPercentage: Number(b.fromPercentage)
				};
				if (b.toPercentage !== null && b.toPercentage !== "" && b.toPercentage !== undefined) {
					oBand.toPercentage = Number(b.toPercentage);
				}
				return oBand;
			});

			this.put("master/threshold-levels", {
				factoryId: Number(this._factoryId()) || 0,
				bands: aBands,
				updatedBy: "dashboard"
			}).then(function (aSaved) {
				that._model.setProperty("/master/thresholds", aSaved);
				MessageToast.show(that.getText("bandsSaved"));
			}).catch(function (err) { that.showError(err); });
		}
	});
});
