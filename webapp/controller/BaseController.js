sap.ui.define([
	"sap/ui/core/mvc/Controller",
	"sap/ui/core/UIComponent",
	"sap/m/MessageBox"
], function (Controller, UIComponent, MessageBox) {
	"use strict";

	/**
	 * Shared plumbing: routing, the app model, and JSON calls to the planning
	 * API with consistent error surfacing.
	 */
	return Controller.extend("kss.spp.controller.BaseController", {

		getRouter: function () {
			return UIComponent.getRouterFor(this);
		},

		getAppModel: function () {
			return this.getOwnerComponent().getModel("app");
		},

		getApiBase: function () {
			return this.getOwnerComponent().getApiBase();
		},

		getText: function (sKey, aArgs) {
			return this.getOwnerComponent().getModel("i18n").getResourceBundle()
				.getText(sKey, aArgs);
		},

		/**
		 * Calls the API and rejects with the server's own message, so a
		 * capacity rule or a version conflict reaches the user verbatim
		 * instead of becoming "request failed".
		 */
		callApi: function (sMethod, sPath, oBody) {
			var oOptions = {
				method: sMethod,
				headers: { "Accept": "application/json" }
			};
			if (oBody !== undefined) {
				oOptions.headers["Content-Type"] = "application/json";
				oOptions.body = JSON.stringify(oBody);
			}

			return fetch(this.getApiBase() + "/api/v1/" + sPath, oOptions)
				.then(function (res) {
					return res.json()
						.catch(function () { return null; })
						.then(function (body) {
							if (!res.ok) {
								var sMessage = (body && body.error) || ("HTTP " + res.status);
								var oError = new Error(sMessage);
								oError.status = res.status;
								throw oError;
							}
							return body;
						});
				});
		},

		get: function (sPath) {
			return this.callApi("GET", sPath);
		},

		post: function (sPath, oBody) {
			return this.callApi("POST", sPath, oBody);
		},

		put: function (sPath, oBody) {
			return this.callApi("PUT", sPath, oBody);
		},

		/**
		 * Shows a server error. A 409 is a concurrent edit rather than a
		 * fault, so it is worded as one.
		 */
		showError: function (oError) {
			if (oError && oError.status === 409) {
				MessageBox.warning(oError.message, {
					title: this.getText("conflictTitle")
				});
				return;
			}
			MessageBox.error((oError && oError.message) || String(oError));
		},

		/**
		 * Warnings come back on a successful save: the change was stored, but
		 * something about it deserves attention.
		 */
		showWarnings: function (aWarnings) {
			if (!aWarnings || !aWarnings.length) {
				return false;
			}
			MessageBox.warning(aWarnings.map(function (w) { return w.message; }).join("\n\n"), {
				title: this.getText("savedWithWarnings")
			});
			return true;
		}
	});
});
