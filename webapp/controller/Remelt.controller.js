sap.ui.define([
	"kss/spp/controller/BaseController",
	"sap/m/MessageToast",
	"sap/m/MessageBox",
	"kss/spp/model/formatter"
], function (BaseController, MessageToast, MessageBox, formatter) {
	"use strict";

	/**
	 * Raw sugar to remelt (requirement section 9):
	 *
	 *   select source warehouse -> select product/batch -> check available
	 *   stock -> issue raw sugar -> remelt
	 */
	return BaseController.extend("kss.spp.controller.Remelt", {

		formatter: formatter,

		onInit: function () {
			this._model = this.getAppModel();
			if (!this._model.getProperty("/remelt")) {
				this._model.setProperty("/remelt", {
					warehouses: [],
					storageCode: "",
					stock: [],
					totalAvailable: 0,
					selected: null,
					quantity: 0,
					reference: "",
					history: []
				});
			}
			this.getRouter().getRoute("remelt").attachPatternMatched(this._onRouteMatched, this);
		},

		_onRouteMatched: function () {
			this._loadWarehouses().then(this._loadStock.bind(this));
		},

		_factoryQuery: function () {
			var sFactory = this._model.getProperty("/filters/factoryId");
			return sFactory ? "factoryId=" + sFactory + "&" : "";
		},

		/** Only raw sugar warehouses can feed the remelt. */
		_loadWarehouses: function () {
			var that = this;
			this._model.setProperty("/busy", true);

			return this.get("master/storage-locations?" + this._factoryQuery() +
				"storageType=RAW_SUGAR_WAREHOUSE")
				.then(function (aWarehouses) {
					that._model.setProperty("/remelt/warehouses", aWarehouses);
					if (aWarehouses.length && !that._model.getProperty("/remelt/storageCode")) {
						that._model.setProperty("/remelt/storageCode", aWarehouses[0].storageCode);
					}
				})
				.catch(function (err) { that.showError(err); })
				.finally(function () { that._model.setProperty("/busy", false); });
		},

		_loadStock: function () {
			var that = this;
			var sCode = this._model.getProperty("/remelt/storageCode");
			if (!sCode) {
				this._model.setProperty("/remelt/stock", []);
				return Promise.resolve();
			}

			this._model.setProperty("/busy", true);
			return Promise.all([
				this.get("inventory/balances?" + this._factoryQuery() +
					"storageCode=" + encodeURIComponent(sCode) + "&nonZeroOnly=true"),
				this.get("inventory/movements?" + this._factoryQuery() + "limit=20")
			]).then(function (a) {
				// The API returns on-hand and reserved; available is the
				// difference, which is what may actually be issued.
				var aStock = (a[0] || []).map(function (b) {
					return Object.assign({}, b, {
						availableWeight: b.weightQuantity - b.reservedWeightQuantity,
						availablePackages: b.packageQuantity - b.reservedPackageQuantity
					});
				});
				that._model.setProperty("/remelt/stock", aStock);
				that._model.setProperty("/remelt/totalAvailable",
					aStock.reduce(function (sum, b) { return sum + b.availableWeight; }, 0));

				that._model.setProperty("/remelt/history",
					(a[1] || []).filter(function (m) { return m.movementTypeCode === "REMELT_ISSUE"; }));

				// A stale selection from the previous warehouse would let the
				// operator issue against stock that is no longer listed.
				that._model.setProperty("/remelt/selected", null);
				var oTable = that.byId("remeltStockTable");
				if (oTable) {
					oTable.removeSelections(true);
				}
			}).catch(function (err) {
				that.showError(err);
			}).finally(function () {
				that._model.setProperty("/busy", false);
			});
		},

		onWarehouseChange: function () {
			this._loadStock();
		},

		onRefresh: function () {
			this._loadStock();
		},

		onStockSelect: function (oEvent) {
			var oItem = oEvent.getParameter("listItem");
			if (!oItem) {
				this._model.setProperty("/remelt/selected", null);
				return;
			}
			var oStock = oItem.getBindingContext("app").getObject();
			this._model.setProperty("/remelt/selected", oStock);
			// Default to the whole available quantity: issuing everything is
			// the common case, and it is easier to reduce than to type.
			this._model.setProperty("/remelt/quantity", oStock.availableWeight);
		},

		onIssue: function () {
			var that = this;
			var oSelected = this._model.getProperty("/remelt/selected");
			var fQty = Number(this._model.getProperty("/remelt/quantity"));

			if (!oSelected) {
				MessageToast.show(this.getText("selectStockFirst"));
				return;
			}
			if (!isFinite(fQty) || fQty <= 0) {
				MessageToast.show(this.getText("enterQuantity"));
				return;
			}

			var sConfirm = this.getText("confirmIssue", [
				formatter.tons(fQty), oSelected.productName, oSelected.storageCode
			]);

			MessageBox.confirm(sConfirm, {
				title: this.getText("issueToRemelt"),
				onClose: function (sAction) {
					if (sAction !== MessageBox.Action.OK) {
						return;
					}
					that._postIssue(oSelected, fQty);
				}
			});
		},

		_postIssue: function (oSelected, fQty) {
			var that = this;
			var sFactory = this._model.getProperty("/filters/factoryId");

			this._model.setProperty("/busy", true);
			this.post("inventory/movements", {
				factoryId: Number(sFactory) || 1,
				movementType: "REMELT_ISSUE",
				storageCode: oSelected.storageCode,
				productCode: oSelected.productCode,
				packagingCode: oSelected.packagingCode,
				batchNo: oSelected.batchNo || "",
				weightQuantity: fQty,
				referenceDoc: this._model.getProperty("/remelt/reference") || "",
				remark: "Issued to remelt from the remelt screen",
				postedBy: "dashboard"
			}).then(function (oResult) {
				if (!oResult.posted) {
					// The capacity engine refused it; show why, verbatim.
					var aFindings = (oResult.validation && oResult.validation.findings) || [];
					MessageBox.error(aFindings.map(function (f) { return f.message; }).join("\n\n") ||
						that.getText("issueRefused"));
					return;
				}
				MessageToast.show(that.getText("issued", [formatter.tons(fQty)]));
				that._model.setProperty("/remelt/quantity", 0);
				that._model.setProperty("/remelt/reference", "");
			}).catch(function (err) {
				that.showError(err);
			}).finally(function () {
				that._model.setProperty("/busy", false);
				that._loadStock();
			});
		},

		onNavBack: function () {
			this.getRouter().navTo("dashboard");
		}
	});
});
