sap.ui.define([
	"sap/ui/core/mvc/Controller",
	"sap/ui/core/UIComponent",
	"sap/ui/core/Fragment",
	"sap/m/MessageBox",
	"sap/m/MessageToast"
], function (Controller, UIComponent, Fragment, MessageBox, MessageToast) {
	"use strict";

	/**
	 * Shared plumbing: routing, the app model, the signed-in user, and JSON
	 * calls to the planning API with consistent error surfacing.
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

		/** The signed-in user and what they may do. */
		getSession: function () {
			return this.getAppModel().getProperty("/session");
		},

		/**
		 * Calls the API and rejects with the server's own message, so a
		 * capacity rule or a version conflict reaches the user verbatim
		 * instead of becoming "request failed".
		 */
		callApi: function (sMethod, sPath, oBody) {
			var that = this;
			var oOptions = {
				method: sMethod,
				// The session lives in a cookie the page cannot read, so it
				// has to be sent by the browser rather than attached here.
				credentials: "same-origin",
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
								// 401 means the session ended while this page
								// stayed open. Nothing the screen can do with
								// that, so hand it to the login flow — except
								// on the sign-in call itself, where a 401 is
								// simply a wrong password and belongs to the
								// login screen already showing.
								if (res.status === 401 && sPath.indexOf("auth/") !== 0) {
									that.getOwnerComponent().requireSignIn();
								}
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
		 * fault, and a 403 is an answer rather than a failure, so both are
		 * worded as what they are.
		 */
		showError: function (oError) {
			if (oError && oError.status === 409) {
				MessageBox.warning(oError.message, {
					title: this.getText("conflictTitle")
				});
				return;
			}
			if (oError && oError.status === 403) {
				MessageBox.information(oError.message, {
					title: this.getText("notPermittedTitle")
				});
				return;
			}
			if (oError && oError.status === 401) {
				// requireSignIn has already moved the user along.
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
		},

		// --- the signed-in user ----------------------------------------------

		/** Opens the popover behind the user button in every page header. */
		onOpenUserMenu: function (oEvent) {
			var that = this;
			var oButton = oEvent.getSource();

			var pPopover = this._pUserMenu || (this._pUserMenu = Fragment.load({
				id: this.getView().getId(),
				name: "kss.spp.view.UserMenu",
				controller: this
			}).then(function (oPopover) {
				that.getView().addDependent(oPopover);
				return oPopover;
			}));

			return pPopover.then(function (oPopover) {
				oPopover.openBy(oButton);
			});
		},

		onCloseUserMenu: function () {
			var that = this;
			if (this._pUserMenu) {
				this._pUserMenu.then(function (oPopover) {
					oPopover.close();
					that._pUserMenu = null;
					oPopover.destroy();
				});
			}
		},

		/** Ends the session and returns to the login screen. */
		onSignOut: function () {
			var that = this;
			this.onCloseUserMenu();

			this.post("auth/logout", {})
				.catch(function () {
					// The session is gone either way; the cookie is cleared by
					// the server when it can be, and by the login screen when
					// it cannot.
				})
				.then(function () {
					return that.getOwnerComponent().refreshSession().catch(function () { });
				})
				.then(function () {
					MessageToast.show(that.getText("signedOut"));
					that.getRouter().navTo("login", {}, true);
				});
		},

		/** Opens the user administration screen. */
		onManageUsers: function () {
			this.onCloseUserMenu();
			this.getRouter().navTo("users");
		},

		/** Opens the change-password dialog. */
		onChangePassword: function () {
			var that = this;
			this.onCloseUserMenu();

			var pDialog = this._pPasswordDialog || (this._pPasswordDialog = Fragment.load({
				id: this.getView().getId() + "--pw",
				name: "kss.spp.view.ChangePasswordDialog",
				controller: this
			}).then(function (oDialog) {
				that.getView().addDependent(oDialog);
				return oDialog;
			}));

			return pDialog.then(function (oDialog) {
				that._passwordFields().forEach(function (oField) {
					if (oField) { oField.setValue(""); }
				});
				oDialog.open();
			});
		},

		onCancelChangePassword: function () {
			if (this._pPasswordDialog) {
				this._pPasswordDialog.then(function (oDialog) { oDialog.close(); });
			}
		},

		onSubmitChangePassword: function () {
			var that = this;
			var aFields = this._passwordFields();
			var sCurrent = aFields[0] ? aFields[0].getValue() : "";
			var sNew = aFields[1] ? aFields[1].getValue() : "";
			var sRepeat = aFields[2] ? aFields[2].getValue() : "";

			if (sNew !== sRepeat) {
				MessageBox.warning(this.getText("passwordsDiffer"));
				return;
			}

			this.post("auth/change-password", { currentPassword: sCurrent, newPassword: sNew })
				.then(function (oResult) {
					that.onCancelChangePassword();
					MessageBox.information(oResult.message, {
						title: that.getText("passwordChanged"),
						onClose: function () {
							that.getOwnerComponent().refreshSession()
								.catch(function () { })
								.then(function () {
									that.getRouter().navTo("login", {}, true);
								});
						}
					});
				})
				.catch(function (err) { that.showError(err); });
		},

		/** The three fields of the change-password dialog, in order. */
		_passwordFields: function () {
			var sFragmentId = this.getView().getId() + "--pw";
			return ["currentPassword", "newPassword", "repeatPassword"].map(function (sId) {
				return Fragment.byId(sFragmentId, sId);
			});
		}
	});
});
